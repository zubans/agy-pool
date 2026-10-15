// Package httpapi — HTTP-маршруты сервиса.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps — зависимости маршрутизатора.
type Deps struct {
	Health  *Health
	Metrics *Metrics
}

// NewRouter собирает маршрутизатор со служебными маршрутами /healthz и /metrics.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// RealIP не подключён: перед сервисом нет обратного прокси, клиенты ходят напрямую
	// по сети agy-pool, и заголовки X-Forwarded-For / X-Real-IP мог бы подделать любой клиент.
	r.Use(requestLogger)
	r.Use(d.Metrics.Middleware)
	r.Use(middleware.Recoverer)

	r.Method(http.MethodGet, "/healthz", d.Health)
	r.Method(http.MethodGet, "/metrics", d.Metrics.Handler())

	return r
}

// requestLogger пишет каждый запрос в журнал. Служебные маршруты — на уровне debug,
// чтобы проверки здоровья и сбор метрик не засоряли журнал.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		status := statusOf(ww)
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError && r.URL.Path != "/healthz":
			level = slog.LevelError
		case r.URL.Path == "/healthz" || r.URL.Path == "/metrics":
			level = slog.LevelDebug
		}

		slog.Log(r.Context(), level, "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", routePattern(r),
			"status", status,
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}
