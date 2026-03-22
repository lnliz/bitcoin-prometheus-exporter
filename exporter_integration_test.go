package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func integrationConfigFromEnv(t *testing.T) config {
	t.Helper()

	if os.Getenv("REAL_RPC_TESTS") != "1" {
		t.Skip("skipping real RPC integration tests (set REAL_RPC_TESTS=1 to enable)")
	}

	hostPort := envOrDefault("REAL_RPC_HOST", "bitcoinrpc.xyz:2082")
	user := os.Getenv("REAL_RPC_USER")
	password := os.Getenv("REAL_RPC_PASSWORD")
	scheme := envOrDefault("REAL_RPC_SCHEME", "http")

	if user == "" || password == "" {
		t.Skip("REAL_RPC_USER and REAL_RPC_PASSWORD must be set for real RPC integration tests")
	}

	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		t.Fatalf("invalid REAL_RPC_HOST %q: %v", hostPort, err)
	}

	return config{
		rpcScheme:      scheme,
		rpcHost:        host,
		rpcPort:        port,
		rpcUser:        user,
		rpcPassword:    password,
		userSet:        true,
		passwordSet:    true,
		hashpsBlocks:   []int{1},
		smartFeeBlocks: []int{2},
		timeout:        15,
		banAddrMetrics: false,
		banAddrLimit:   100,
	}
}

func TestIntegrationRefreshMetricsWithRealRPC(t *testing.T) {
	cfg := integrationConfigFromEnv(t)
	exp := newExporter(cfg)

	if err := exp.refreshMetrics(context.Background()); err != nil {
		t.Fatalf("refreshMetrics failed: %v", err)
	}

	if got := testutil.ToFloat64(exp.metrics.blocks); got <= 0 {
		t.Fatalf("bitcoin_blocks should be > 0, got %v", got)
	}
	if got := testutil.ToFloat64(exp.metrics.peers); got < 0 {
		t.Fatalf("bitcoin_peers should be >= 0, got %v", got)
	}
	if got := testutil.ToFloat64(exp.metrics.warnings); got != 0 && got != 1 {
		t.Fatalf("bitcoin_warnings should be 0 or 1, got %v", got)
	}
}

func TestIntegrationHandleMetricsWithRealRPC(t *testing.T) {
	cfg := integrationConfigFromEnv(t)
	exp := newExporter(cfg)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	exp.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "bitcoin_blocks") {
		t.Fatalf("metrics output missing bitcoin_blocks")
	}
	if !strings.Contains(body, "bitcoin_exporter_process_time_seconds") {
		t.Fatalf("metrics output missing bitcoin_exporter_process_time_seconds")
	}
}
