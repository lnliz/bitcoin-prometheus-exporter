package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type rpcClient struct {
	url      string
	user     string
	password string
	timeout  time.Duration
	client   *http.Client
	idCtr    atomic.Uint64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("RPC error %d: %s", e.Code, e.Message)
}

func (e *rpcError) IsWarmup() bool {
	return e.Code == -28
}

func newRPCClient(url, user, password string, timeout time.Duration) *rpcClient {
	return &rpcClient{
		url:      url,
		user:     user,
		password: password,
		timeout:  timeout,
		client:   &http.Client{},
	}
}

func jsonBool(v any) float64 {
	switch b := v.(type) {
	case bool:
		if b {
			return 1
		}
		return 0
	case float64:
		if b != 0 {
			return 1
		}
		return 0
	case int:
		if b != 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func (c *rpcClient) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	id := c.idCtr.Add(1)
	reqBody := rpcRequest{
		JSONRPC: "1.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	slog.Debug("RPC call", "method", method, "params", params)

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.password != "" {
		req.SetBasicAuth(c.user, c.password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpc request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		if !isHTTPSuccess(resp.StatusCode) {
			return nil, newHTTPStatusError(resp.StatusCode, resp.Status, respBody)
		}
		return nil, &jsonDecodeError{cause: err, body: string(respBody)}
	}

	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}
	if !isHTTPSuccess(resp.StatusCode) {
		return nil, newHTTPStatusError(resp.StatusCode, resp.Status, respBody)
	}
	if rpcResp.ID != id {
		return nil, fmt.Errorf("rpc response id mismatch: got %d want %d", rpcResp.ID, id)
	}

	slog.Debug("RPC result", "method", method, "result", string(rpcResp.Result))

	return rpcResp.Result, nil
}

func isHTTPSuccess(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

type httpStatusError struct {
	statusCode int
	status     string
	body       string
}

func newHTTPStatusError(statusCode int, status string, body []byte) *httpStatusError {
	const maxBody = 512
	bodyText := strings.TrimSpace(string(body))
	if len(bodyText) > maxBody {
		bodyText = bodyText[:maxBody] + "..."
	}
	return &httpStatusError{
		statusCode: statusCode,
		status:     status,
		body:       bodyText,
	}
}

func (e *httpStatusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("rpc http status %s", e.status)
	}
	return fmt.Sprintf("rpc http status %s: %s", e.status, e.body)
}

func (e *httpStatusError) retryable() bool {
	return e.statusCode == http.StatusRequestTimeout ||
		e.statusCode == http.StatusTooManyRequests ||
		e.statusCode >= http.StatusInternalServerError
}

type jsonDecodeError struct {
	cause error
	body  string
}

func (e *jsonDecodeError) Error() string {
	return fmt.Sprintf("json decode error: %v (body: %s)", e.cause, e.body)
}

func isRetryable(err error) bool {
	var rpcErr *rpcError
	if errors.As(err, &rpcErr) {
		return rpcErr.IsWarmup()
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.retryable()
	}
	if netErr, ok := err.(net.Error); ok {
		if netErr.Timeout() {
			return true
		}
	}
	var connErr *net.OpError
	if errors.As(err, &connErr) {
		return true
	}
	if isConnectionRefused(err) {
		return true
	}
	return false
}

func isConnectionRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "connection refused")
}

type retryableRPC struct {
	client       *rpcClient
	timeout      time.Duration
	onRetry      func(err error)
	maxBackoff   time.Duration
	initialDelay time.Duration
}

func (r *retryableRPC) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	deadline := time.Now().Add(r.timeout)
	delay := r.initialDelay

	for {
		result, err := r.client.call(ctx, method, params...)
		if err == nil {
			return result, nil
		}

		if !isRetryable(err) {
			return nil, err
		}

		if r.onRetry != nil {
			r.onRetry(err)
		}

		if time.Now().Add(delay).After(deadline) {
			return nil, fmt.Errorf("retry timeout after %v: %w", r.timeout, err)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		delay = min(time.Duration(float64(delay)*2), r.maxBackoff)
	}
}

func errorTypeName(err error) string {
	if _, ok := errors.AsType[*rpcError](err); ok {
		return "rpc_error"
	}
	if netErr, ok := errors.AsType[net.Error](err); ok {
		if netErr.Timeout() {
			return "timeout"
		}
		return "connection_error"
	}
	if _, ok := errors.AsType[*httpStatusError](err); ok {
		return "http_status"
	}
	return "connection_error"
}

func jsonFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			slog.Debug("Failed to parse json number", "value", n, "error", err)
			return 0
		}
		return f
	case nil:
		return 0
	default:
		slog.Debug("Unexpected numeric type", "type", fmt.Sprintf("%T", n), "value", n)
		return 0
	}
}
