package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsRetryable(t *testing.T) {
	if !isRetryable(&rpcError{Code: -28, Message: "Loading block index..."}) {
		t.Fatalf("expected warmup rpc error to be retryable")
	}
	if !isRetryable(&httpStatusError{statusCode: http.StatusServiceUnavailable}) {
		t.Fatalf("expected HTTP 503 error to be retryable")
	}
	if isRetryable(&httpStatusError{statusCode: http.StatusUnauthorized}) {
		t.Fatalf("did not expect HTTP 401 error to be retryable")
	}
	if !isRetryable(testNetErr{timeout: true}) {
		t.Fatalf("expected timeout net error to be retryable")
	}
	if !isRetryable(&net.OpError{Op: "dial", Err: errors.New("i/o timeout")}) {
		t.Fatalf("expected net.OpError to be retryable")
	}
	if !isRetryable(errors.New("dial tcp 127.0.0.1:8332: connection refused")) {
		t.Fatalf("expected connection refused to be retryable")
	}
	if isRetryable(errors.New("some other error")) {
		t.Fatalf("did not expect generic error to be retryable")
	}
}

func TestRPCClientCallSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "user" || pass != "pwd" {
			t.Fatalf("unexpected basic auth: ok=%v user=%q pass=%q", ok, user, pass)
		}

		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Method != "uptime" {
			t.Fatalf("unexpected method: %q", req.Method)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": 123.0,
			"error":  nil,
			"id":     req.ID,
		})
	}))
	defer srv.Close()

	client := newRPCClient(srv.URL, "user", "pwd", 2*time.Second)
	raw, err := client.call(context.Background(), "uptime")
	if err != nil {
		t.Fatalf("rpc call failed: %v", err)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("failed to decode result: %v", err)
	}
	if value != 123 {
		t.Fatalf("unexpected value: %v", value)
	}
}

func TestRPCClientCallRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": nil,
			"error": map[string]any{
				"code":    -1,
				"message": "boom",
			},
			"id": req.ID,
		})
	}))
	defer srv.Close()

	client := newRPCClient(srv.URL, "", "", 2*time.Second)
	_, err := client.call(context.Background(), "getblockchaininfo")
	if err == nil {
		t.Fatalf("expected rpc error")
	}
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected *rpcError, got %T", err)
	}
}

func TestRPCClientCallRPCErrorWithHTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": nil,
			"error": map[string]any{
				"code":    -28,
				"message": "Loading block index...",
			},
			"id": req.ID,
		})
	}))
	defer srv.Close()

	client := newRPCClient(srv.URL, "", "", 2*time.Second)
	_, err := client.call(context.Background(), "getblockchaininfo")
	if err == nil {
		t.Fatalf("expected rpc error")
	}
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected *rpcError, got %T", err)
	}
	if !rpcErr.IsWarmup() {
		t.Fatalf("expected warmup error, got %v", rpcErr)
	}
}

func TestRPCClientCallHTTPStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := newRPCClient(srv.URL, "", "", 2*time.Second)
	_, err := client.call(context.Background(), "getblockchaininfo")
	if err == nil {
		t.Fatalf("expected HTTP status error")
	}
	var statusErr *httpStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected *httpStatusError, got %T", err)
	}
	if statusErr.statusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected status code: got %d want %d", statusErr.statusCode, http.StatusUnauthorized)
	}
}

func TestRPCClientCallResponseIDMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": 123.0,
			"error":  nil,
			"id":     999,
		})
	}))
	defer srv.Close()

	client := newRPCClient(srv.URL, "", "", 2*time.Second)
	_, err := client.call(context.Background(), "uptime")
	if err == nil {
		t.Fatalf("expected response ID mismatch error")
	}
	if !strings.Contains(err.Error(), "response id mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRPCClientCallInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	client := newRPCClient(srv.URL, "", "", 2*time.Second)
	_, err := client.call(context.Background(), "getblockchaininfo")
	if err == nil {
		t.Fatalf("expected error")
	}
	var decodeErr *jsonDecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("expected *jsonDecodeError, got %T", err)
	}
}

func TestRetryableRPCCallStopsDuringBackoffWhenContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": nil,
			"error": map[string]any{
				"code":    -28,
				"message": "Loading block index...",
			},
			"id": req.ID,
		})
	}))
	defer srv.Close()

	client := newRPCClient(srv.URL, "", "", 2*time.Second)
	rpc := &retryableRPC{
		client:       client,
		timeout:      24 * time.Hour,
		initialDelay: time.Hour,
		maxBackoff:   time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	start := time.Now()
	_, err := rpc.call(ctx, "getblockchaininfo")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("retry backoff did not stop promptly: %v", elapsed)
	}
}
