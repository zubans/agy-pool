package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// DefaultCheckTimeout — сколько ждать одну проверку зависимости.
const DefaultCheckTimeout = 2 * time.Second

// Checker — проверка одной зависимости сервиса для /healthz.
type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

// HealthResponse — тело ответа /healthz.
type HealthResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Health выполняет проверки параллельно и отвечает 200, если все прошли, иначе 503.
type Health struct {
	checkers []Checker
	timeout  time.Duration
	metrics  *Metrics
}

// NewHealth создаёт обработчик /healthz. metrics может быть nil.
func NewHealth(timeout time.Duration, metrics *Metrics, checkers ...Checker) *Health {
	if timeout <= 0 {
		timeout = DefaultCheckTimeout
	}

	return &Health{checkers: checkers, timeout: timeout, metrics: metrics}
}

// Check выполняет все проверки и возвращает итог.
func (h *Health) Check(ctx context.Context) HealthResponse {
	resp := HealthResponse{Status: "ok", Checks: make(map[string]string, len(h.checkers))}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, c := range h.checkers {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, h.timeout)
			defer cancel()

			err := c.Check(ctx)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				resp.Status = "fail"
				resp.Checks[c.Name()] = err.Error()
			} else {
				resp.Checks[c.Name()] = "ok"
			}
			if h.metrics != nil {
				h.metrics.SetDependencyUp(c.Name(), err == nil)
			}
		})
	}
	wg.Wait()

	return resp
}

// ServeHTTP отвечает на GET /healthz.
// Проверки не прерываются, если вызывающий оборвал соединение: иначе они упали бы
// с context canceled и записали в метрику agy_pool_dependency_up ложный 0.
// Каждая проверка всё равно ограничена своим таймаутом.
func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := h.Check(context.WithoutCancel(r.Context()))

	code := http.StatusOK
	if resp.Status != "ok" {
		code = http.StatusServiceUnavailable
		slog.WarnContext(r.Context(), "health check failed", "checks", resp.Checks)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

// Pinger — запрос проверки БД (sqlc-запрос Ping).
type Pinger interface {
	Ping(ctx context.Context) (int32, error)
}

// PostgresChecker проверяет PostgreSQL запросом SELECT 1.
type PostgresChecker struct {
	Queries Pinger
}

// Name — название зависимости.
func (PostgresChecker) Name() string { return "postgres" }

// Check выполняет запрос и проверяет результат.
func (c PostgresChecker) Check(ctx context.Context) error {
	v, err := c.Queries.Ping(ctx)
	if err != nil {
		return err
	}
	if v != 1 {
		return fmt.Errorf("unexpected ping result %d", v)
	}

	return nil
}

// NATSChecker проверяет подключение к NATS и что JetStream включён и отвечает.
type NATSChecker struct {
	Conn      *nats.Conn
	JetStream jetstream.JetStream
}

// Name — название зависимости.
func (NATSChecker) Name() string { return "nats" }

// Check проверяет состояние подключения и запрашивает сведения об аккаунте JetStream.
func (c NATSChecker) Check(ctx context.Context) error {
	if c.Conn == nil {
		return errors.New("not connected")
	}
	if s := c.Conn.Status(); s != nats.CONNECTED {
		return fmt.Errorf("connection status %s", s)
	}
	if _, err := c.JetStream.AccountInfo(ctx); err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}

	return nil
}
