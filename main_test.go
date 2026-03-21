package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestParseIntList(t *testing.T) {
	got := parseIntList("1, 2, x, ,3")
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseIntList mismatch: got %v want %v", got, want)
	}
}

func TestParseConfFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "bitcoin.conf")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	if _, err := f.WriteString("rpcuser=alice\nrpcpassword=secret\nrpcconnect=127.0.0.1\nrpcport=8332\n"); err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	user, pass, host, port, err := parseConfFile(f.Name())
	if err != nil {
		t.Fatalf("parseConfFile error: %v", err)
	}
	if user != "alice" || pass != "secret" || host != "127.0.0.1" || port != "8332" {
		t.Fatalf("parseConfFile mismatch: %q %q %q %q", user, pass, host, port)
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_ENV_OR_DEFAULT", "value")
	if got := envOrDefault("TEST_ENV_OR_DEFAULT", "fallback"); got != "value" {
		t.Fatalf("envOrDefault should return env value, got %q", got)
	}
	if got := envOrDefault("TEST_ENV_OR_DEFAULT_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("envOrDefault should return fallback, got %q", got)
	}
}

func TestEnvIntOrDefault(t *testing.T) {
	t.Setenv("TEST_ENV_INT_OR_DEFAULT", "42")
	if got := envIntOrDefault("TEST_ENV_INT_OR_DEFAULT", 7); got != 42 {
		t.Fatalf("envIntOrDefault should parse integer, got %d", got)
	}

	t.Setenv("TEST_ENV_INT_OR_DEFAULT", "not-a-number")
	if got := envIntOrDefault("TEST_ENV_INT_OR_DEFAULT", 7); got != 7 {
		t.Fatalf("envIntOrDefault should fall back on invalid integer, got %d", got)
	}
}

func TestParseSlogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"DEBUG", "DEBUG"},
		{"warn", "WARN"},
		{"CRITICAL", "ERROR"},
		{"other", "INFO"},
	}

	for _, tc := range cases {
		got := parseSlogLevel(tc.in).String()
		if got != tc.want {
			t.Fatalf("parseSlogLevel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHandleRoot(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handleRoot(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status for root: got %d want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("unexpected content type: %q", ct)
	}
	if rr.Body.String() != rootPageHTML {
		t.Fatalf("unexpected root body")
	}
}

func TestHandleRootNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rr := httptest.NewRecorder()

	handleRoot(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("unexpected status for non-root: got %d want %d", rr.Code, http.StatusNotFound)
	}
}
