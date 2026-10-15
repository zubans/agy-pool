package httpapi

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/zubans/agy-pool/internal/version"
)

// unmatchedRoute — метка маршрута для запросов, не попавших ни в один маршрут.
const unmatchedRoute = "unmatched"

// Metrics — метрики сервиса в собственном реестре Prometheus.
type Metrics struct {
	Registry *prometheus.Registry

	requests     *prometheus.CounterVec
	duration     *prometheus.HistogramVec
	dependencyUp *prometheus.GaugeVec
}

// NewMetrics создаёт реестр со стандартными метриками Go и процесса и метриками HTTP.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agy_pool_http_requests_total",
			Help: "HTTP requests by method, route template and status code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "agy_pool_http_request_duration_seconds",
			Help:    "HTTP request duration by method and route template.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600},
		}, []string{"method", "route"}),
		dependencyUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "agy_pool_dependency_up",
			Help: "Result of the last health check of a dependency: 1 — up, 0 — down.",
		}, []string{"dependency"}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "agy_pool_build_info",
		Help: "Build information; the value is always 1.",
	}, []string{"version", "go_version"})
	buildInfo.WithLabelValues(version.Version, runtime.Version()).Set(1)

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests,
		m.duration,
		m.dependencyUp,
		buildInfo,
	)

	return m
}

// Handler отдаёт метрики в формате Prometheus.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{Registry: m.Registry})
}

// SetDependencyUp записывает результат проверки зависимости.
func (m *Metrics) SetDependencyUp(dependency string, up bool) {
	v := 0.0
	if up {
		v = 1
	}
	m.dependencyUp.WithLabelValues(dependency).Set(v)
}

// Middleware считает запросы и их длительность. Маршрут берётся шаблоном chi
// (/v1/jobs/{id}), а не путём, чтобы не плодить ряды метрик.
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		route := routePattern(r)
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(statusOf(ww))).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if p := rctx.RoutePattern(); p != "" {
			return p
		}
	}

	return unmatchedRoute
}

func statusOf(ww middleware.WrapResponseWriter) int {
	if s := ww.Status(); s != 0 {
		return s
	}

	return http.StatusOK
}
