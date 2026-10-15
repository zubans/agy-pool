package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zubans/agy-pool/internal/db"
	"github.com/zubans/agy-pool/internal/db/sqlc"
	"github.com/zubans/agy-pool/internal/httpapi"
	"github.com/zubans/agy-pool/internal/natsx"
	"github.com/zubans/agy-pool/internal/testutil"
)

type stubChecker struct {
	name  string
	err   error
	delay time.Duration
}

func (s stubChecker) Name() string { return s.name }

func (s stubChecker) Check(ctx context.Context) error {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return s.err
}

func newServer(t *testing.T, checkers ...httpapi.Checker) (*httptest.Server, *httpapi.Metrics) {
	t.Helper()

	metrics := httpapi.NewMetrics()
	health := httpapi.NewHealth(500*time.Millisecond, metrics, checkers...)
	srv := httptest.NewServer(httpapi.NewRouter(httpapi.Deps{Health: health, Metrics: metrics}))
	t.Cleanup(srv.Close)

	return srv, metrics
}

func getHealth(t *testing.T, url string) (int, httpapi.HealthResponse) {
	t.Helper()

	resp, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	var body httpapi.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	return resp.StatusCode, body
}

func TestHealthAllOK(t *testing.T) {
	srv, _ := newServer(t, stubChecker{name: "postgres"}, stubChecker{name: "nats"})

	code, body := getHealth(t, srv.URL)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body.Status != "ok" || body.Checks["postgres"] != "ok" || body.Checks["nats"] != "ok" {
		t.Fatalf("body = %+v", body)
	}
}

func TestHealthOneFails(t *testing.T) {
	srv, _ := newServer(t,
		stubChecker{name: "postgres"},
		stubChecker{name: "nats", err: errors.New("connection status RECONNECTING")},
	)

	code, body := getHealth(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if body.Status != "fail" {
		t.Errorf("status field = %q", body.Status)
	}
	if body.Checks["postgres"] != "ok" {
		t.Errorf("postgres = %q", body.Checks["postgres"])
	}
	if !strings.Contains(body.Checks["nats"], "RECONNECTING") {
		t.Errorf("nats = %q, want the failure reason", body.Checks["nats"])
	}
}

func TestHealthCheckTimesOut(t *testing.T) {
	srv, _ := newServer(t, stubChecker{name: "postgres", delay: 5 * time.Second})

	start := time.Now()
	code, body := getHealth(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("health check took %v, the per-check timeout was not applied", elapsed)
	}
	if !strings.Contains(body.Checks["postgres"], "deadline") {
		t.Errorf("postgres = %q", body.Checks["postgres"])
	}
}

// TestHealthIgnoresCallerCancel проверяет, что оборванный вызывающим запрос не роняет
// проверки и не пишет в метрику ложный отказ зависимости.
func TestHealthIgnoresCallerCancel(t *testing.T) {
	metrics := httpapi.NewMetrics()
	health := httpapi.NewHealth(time.Second, metrics, stubChecker{name: "postgres", delay: 50 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	health.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
}

// TestHealthWithRealDependencies поднимает PostgreSQL и NATS и проверяет, что /healthz
// отвечает 200, затем 503 после остановки NATS, снова 200 после его возврата
// (клиент переподключается сам) и 503 после остановки PostgreSQL.
func TestHealthWithRealDependencies(t *testing.T) {
	pg := testutil.StartPostgres(t)
	ns := testutil.StartNATS(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := db.Open(ctx, pg.URL)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(pool.Close)

	nc, js, err := natsx.Connect(ns.URL, "agy-pool-test")
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	t.Cleanup(nc.Close)

	srv, _ := newServer(t,
		httpapi.PostgresChecker{Queries: sqlc.New(pool)},
		httpapi.NATSChecker{Conn: nc, JetStream: js},
	)

	waitFor(t, func() bool {
		code, _ := getHealth(t, srv.URL)

		return code == http.StatusOK
	}, "healthz 200 with live postgres and nats")

	stopTimeout := 5 * time.Second
	if err := ns.Container.Stop(ctx, &stopTimeout); err != nil {
		t.Fatalf("stop nats: %v", err)
	}
	waitFor(t, func() bool {
		code, body := getHealth(t, srv.URL)

		return code == http.StatusServiceUnavailable && body.Checks["nats"] != "ok" && body.Checks["postgres"] == "ok"
	}, "healthz 503 with nats stopped")

	// После возврата NATS клиент переподключается сам, без перезапуска сервиса.
	if err := ns.Container.Start(ctx); err != nil {
		t.Fatalf("start nats again: %v", err)
	}
	waitFor(t, func() bool {
		code, body := getHealth(t, srv.URL)

		return code == http.StatusOK && body.Checks["nats"] == "ok"
	}, "healthz 200 after nats is back")

	if err := pg.Container.Stop(ctx, &stopTimeout); err != nil {
		t.Fatalf("stop postgres: %v", err)
	}
	waitFor(t, func() bool {
		code, body := getHealth(t, srv.URL)

		return code == http.StatusServiceUnavailable && body.Checks["postgres"] != "ok"
	}, "healthz 503 with postgres stopped")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
