package main

import (
	"encoding/json"
	"testing"
)

type testNetErr struct {
	timeout bool
}

func (e testNetErr) Error() string   { return "net error" }
func (e testNetErr) Timeout() bool   { return e.timeout }
func (e testNetErr) Temporary() bool { return false }

func TestErrorTypeName(t *testing.T) {
	if got := errorTypeName(&rpcError{Code: -28, Message: "warmup"}); got != "rpc_error" {
		t.Fatalf("rpc error type mismatch: %q", got)
	}
	if got := errorTypeName(testNetErr{timeout: true}); got != "timeout" {
		t.Fatalf("timeout error type mismatch: %q", got)
	}
	if got := errorTypeName(testNetErr{timeout: false}); got != "connection_error" {
		t.Fatalf("connection error type mismatch: %q", got)
	}
}

func TestJSONFloat(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{float64(1.5), 1.5},
		{float32(2.5), 2.5},
		{int64(3), 3},
		{uint64(4), 4},
		{json.Number("5.25"), 5.25},
		{"nope", 0},
	}

	for _, tc := range cases {
		if got := jsonFloat(tc.in); got != tc.want {
			t.Fatalf("jsonFloat(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
