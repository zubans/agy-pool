package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsEndpoint(t *testing.T) {
	srv, metrics := newServer(t, stubChecker{name: "postgres"}, stubChecker{name: "nats"})

	before, err := testutil.GatherAndCount(metrics.Registry, "agy_pool_http_requests_total")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for range 2 {
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatalf("GET /healthz: %v", err)
		}
		resp.Body.Close()
	}

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(raw)

	for _, want := range []string{
		"agy_pool_build_info{",
		"go_goroutines ",
		`agy_pool_http_requests_total{code="200",method="GET",route="/healthz"} 2`,
		`agy_pool_http_request_duration_seconds_bucket{method="GET",route="/healthz"`,
		`agy_pool_dependency_up{dependency="nats"} 1`,
		`agy_pool_dependency_up{dependency="postgres"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics has no %q", want)
		}
	}

	after, err := testutil.GatherAndCount(metrics.Registry, "agy_pool_http_requests_total")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if after <= before {
		t.Errorf("request counter series did not appear: before %d, after %d", before, after)
	}
}

func TestMetricsUseRouteTemplateForUnknownPaths(t *testing.T) {
	srv, metrics := newServer(t)

	for _, p := range []string{"/nope/1", "/nope/2", "/nope/3"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
	}

	n, err := testutil.GatherAndCount(metrics.Registry, "agy_pool_http_requests_total")
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if n != 1 {
		t.Errorf("unknown paths produced %d series, want 1 (route=unmatched)", n)
	}
}
