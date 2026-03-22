package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

type mockRPCFixture struct {
	mu                sync.Mutex
	counts            map[string]int
	warnings          string
	invalidJSONMethod string
}

func newMockRPCFixture() *mockRPCFixture {
	return &mockRPCFixture{
		counts:   make(map[string]int),
		warnings: "node warning",
	}
}

func (f *mockRPCFixture) methodCalls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[method]
}

func (f *mockRPCFixture) handler(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.counts[req.Method]++
	f.mu.Unlock()

	if req.Method == f.invalidJSONMethod {
		_, _ = w.Write([]byte("not-json"))
		return
	}

	var result any
	switch req.Method {
	case "uptime":
		result = 1000.0
	case "getmemoryinfo":
		result = map[string]any{
			"locked": map[string]any{
				"used":        1.0,
				"free":        2.0,
				"total":       3.0,
				"locked":      4.0,
				"chunks_used": 5.0,
				"chunks_free": 6.0,
			},
		}
	case "getblockchaininfo":
		result = map[string]any{
			"blocks":               100.0,
			"headers":              101.0,
			"initialblockdownload": false,
			"mediantime":           1.7e9,
			"pruned":               true,
			"pruneheight":          42.0,
			"difficulty":           2.0,
			"size_on_disk":         1234.0,
			"verificationprogress": 0.99,
			"bestblockhash":        "best-hash",
		}
	case "getnetworkinfo":
		result = map[string]any{
			"connections":     8.0,
			"connections_in":  3.0,
			"connections_out": 5.0,
			"relayfee":        0.00001,
			"incrementalfee":  0.00002,
			"version":         230000.0,
			"protocolversion": 70016.0,
			"warnings":        f.warnings,
		}
	case "getchaintips":
		result = []any{
			map[string]any{"height": 100.0, "status": "active"},
			map[string]any{"height": 99.0, "status": "headers-only"},
		}
	case "getmempoolinfo":
		result = map[string]any{
			"bytes":            11.0,
			"size":             2.0,
			"usage":            22.0,
			"total_fee":        0.12345678,
			"maxmempool":       300000000.0,
			"mempoolminfee":    0.00001,
			"fullrbf":          true,
			"unbroadcastcount": 1.0,
		}
	case "getnettotals":
		result = map[string]any{
			"totalbytesrecv": 1000.0,
			"totalbytessent": 2000.0,
		}
	case "getrpcinfo":
		result = map[string]any{
			"active_commands": []any{"getrpcinfo"},
		}
	case "getchaintxstats":
		result = map[string]any{
			"txcount": 99.0,
		}
	case "getblockstats":
		result = map[string]any{
			"total_size":     123.0,
			"total_weight":   456.0,
			"totalfee":       1000.0,
			"txs":            10.0,
			"height":         100.0,
			"ins":            20.0,
			"outs":           30.0,
			"total_out":      5000000000.0,
			"avgfee":         200.0,
			"avgfeerate":     5.0,
			"medianfee":      150.0,
			"utxo_increase":  3.0,
			"swtxs":          8.0,
			"swtotal_size":   100.0,
			"swtotal_weight": 300.0,
		}
	case "listbanned":
		result = []any{
			map[string]any{
				"address":      "1.1.1.1",
				"ban_reason":   "manually added",
				"ban_created":  111.0,
				"banned_until": 222.0,
			},
			map[string]any{
				"address":      "2.2.2.2",
				"ban_reason":   "manually added",
				"ban_created":  333.0,
				"banned_until": 444.0,
			},
		}
	case "estimatesmartfee":
		result = map[string]any{"feerate": 0.0002}
	case "getnetworkhashps":
		result = 12345.0
	case "getindexinfo":
		result = map[string]any{
			"txindex": map[string]any{
				"synced":            true,
				"best_block_height": 100.0,
			},
			"coinstatsindex": map[string]any{
				"synced":            false,
				"best_block_height": 80.0,
			},
		}
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": nil,
			"error": map[string]any{
				"code":    -1,
				"message": "unknown method",
			},
			"id": req.ID,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"result": result,
		"error":  nil,
		"id":     req.ID,
	})
}

func newExporterForTest(t *testing.T, f *mockRPCFixture, banAddrMetrics bool) *exporter {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("failed to parse server URL: %v", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("failed to split host/port: %v", err)
	}

	cfg := config{
		rpcScheme:      u.Scheme,
		rpcHost:        host,
		rpcPort:        port,
		rpcUser:        "user",
		rpcPassword:    "pwd",
		userSet:        true,
		passwordSet:    true,
		hashpsBlocks:   []int{1},
		smartFeeBlocks: []int{2},
		timeout:        5,
		banAddrMetrics: banAddrMetrics,
		banAddrLimit:   1,
		chaintips:      true,
	}

	return newExporter(cfg)
}

func TestExporterRefreshMetricsPopulatesValues(t *testing.T) {
	f := newMockRPCFixture()
	exp := newExporterForTest(t, f, true)

	if err := exp.refreshMetrics(context.Background()); err != nil {
		t.Fatalf("refreshMetrics failed: %v", err)
	}

	if got := testutil.ToFloat64(exp.metrics.blocks); got != 100 {
		t.Fatalf("bitcoin_blocks mismatch: got %v want 100", got)
	}
	if got := testutil.ToFloat64(exp.metrics.headers); got != 101 {
		t.Fatalf("bitcoin_headers mismatch: got %v want 101", got)
	}
	if got := testutil.ToFloat64(exp.metrics.initialBlockDownload); got != 0 {
		t.Fatalf("bitcoin_initial_block_download mismatch: got %v want 0", got)
	}
	if got := testutil.ToFloat64(exp.metrics.pruned); got != 1 {
		t.Fatalf("bitcoin_pruned mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.pruneHeight); got != 42 {
		t.Fatalf("bitcoin_prune_height mismatch: got %v want 42", got)
	}
	if got := testutil.ToFloat64(exp.metrics.warnings); got != 1 {
		t.Fatalf("bitcoin_warnings mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.relayFee); got != 0.00001 {
		t.Fatalf("bitcoin_relay_fee mismatch: got %v want 0.00001", got)
	}
	if got := testutil.ToFloat64(exp.metrics.incrementalFee); got != 0.00002 {
		t.Fatalf("bitcoin_incremental_fee mismatch: got %v want 0.00002", got)
	}
	if got := testutil.ToFloat64(exp.metrics.mempoolMax); got != 300000000 {
		t.Fatalf("bitcoin_mempool_max mismatch: got %v want 300000000", got)
	}
	if got := testutil.ToFloat64(exp.metrics.mempoolTotalFee); got != 0.12345678 {
		t.Fatalf("bitcoin_mempool_total_fee mismatch: got %v want 0.12345678", got)
	}
	if got := testutil.ToFloat64(exp.metrics.mempoolFullRBF); got != 1 {
		t.Fatalf("bitcoin_mempool_fullrbf mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.rpcActive); got != 1 {
		t.Fatalf("bitcoin_rpc_active mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.latestBlockFee); got != 0.00001 {
		t.Fatalf("bitcoin_latest_block_fee mismatch: got %v want 0.00001", got)
	}
	if got := testutil.ToFloat64(exp.metrics.hashpsGauges[1]); got != 12345 {
		t.Fatalf("hashps(1) mismatch: got %v want 12345", got)
	}
	if got := testutil.ToFloat64(exp.metrics.chaintipStatus.WithLabelValues("active")); got != 1 {
		t.Fatalf("bitcoin_chaintips{active} mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.chaintipStatus.WithLabelValues("headers-only")); got != 1 {
		t.Fatalf("bitcoin_chaintips{headers-only} mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.indexSynced.WithLabelValues("txindex")); got != 1 {
		t.Fatalf("bitcoin_index_synced{txindex} mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.indexBestBlockHeight.WithLabelValues("coinstatsindex")); got != 80 {
		t.Fatalf("bitcoin_index_best_block_height{coinstatsindex} mismatch: got %v want 80", got)
	}
	if got := testutil.ToFloat64(exp.metrics.smartFeeGauges[2]); got != 0.0002 {
		t.Fatalf("smart fee(2) mismatch: got %v want 0.0002", got)
	}
	if got := testutil.ToFloat64(exp.metrics.latestBlockAvgFee); got != 0.000002 {
		t.Fatalf("bitcoin_latest_block_avg_fee mismatch: got %v want 0.000002", got)
	}
	if got := testutil.ToFloat64(exp.metrics.latestBlockAvgFeeRate); got != 5 {
		t.Fatalf("bitcoin_latest_block_avg_feerate mismatch: got %v want 5", got)
	}
	if got := testutil.ToFloat64(exp.metrics.latestBlockMedianFee); got != 0.0000015 {
		t.Fatalf("bitcoin_latest_block_median_fee mismatch: got %v want 0.0000015", got)
	}
	if got := testutil.ToFloat64(exp.metrics.latestBlockUTXOIncrease); got != 3 {
		t.Fatalf("bitcoin_latest_block_utxo_increase mismatch: got %v want 3", got)
	}

	if c := testutil.CollectAndCount(exp.metrics.banCreated); c != 1 {
		t.Fatalf("banCreated cardinality mismatch: got %d want 1", c)
	}
	if c := testutil.CollectAndCount(exp.metrics.bannedUntil); c != 1 {
		t.Fatalf("bannedUntil cardinality mismatch: got %d want 1", c)
	}
}

func TestExporterGetBlockStatsUsesCache(t *testing.T) {
	f := newMockRPCFixture()
	exp := newExporterForTest(t, f, false)

	stats1 := exp.getBlockStats(context.Background(), "best-hash")
	stats2 := exp.getBlockStats(context.Background(), "best-hash")

	if stats1 == nil || stats2 == nil {
		t.Fatalf("expected non-nil stats")
	}
	if calls := f.methodCalls("getblockstats"); calls != 1 {
		t.Fatalf("expected getblockstats to be called once, got %d", calls)
	}
}

func TestHandleMetricsSuccess(t *testing.T) {
	f := newMockRPCFixture()
	exp := newExporterForTest(t, f, false)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	exp.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "bitcoin_blocks") {
		t.Fatalf("metrics output missing bitcoin_blocks")
	}
	if !strings.Contains(body, "bitcoin_exporter_process_time_seconds_count") {
		t.Fatalf("metrics output missing process histogram count")
	}
	if !strings.Contains(body, "bitcoin_exporter_build_info") {
		t.Fatalf("metrics output missing bitcoin_exporter_build_info")
	}
	if got := testutil.ToFloat64(exp.metrics.scrapeSuccess); got != 1 {
		t.Fatalf("scrapeSuccess mismatch: got %v want 1", got)
	}
}

func TestHandleMetricsJSONDecodeErrorIncrementsCounter(t *testing.T) {
	f := newMockRPCFixture()
	f.invalidJSONMethod = "getmemoryinfo"
	exp := newExporterForTest(t, f, false)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	exp.handleMetrics(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rr.Code, http.StatusOK)
	}
	if got := testutil.ToFloat64(exp.metrics.exporterErrors.WithLabelValues("json_decode")); got != 1 {
		t.Fatalf("json_decode error counter mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.exporterRPCErrorsByMethod.WithLabelValues("getmemoryinfo", "json_decode")); got != 1 {
		t.Fatalf("rpc method error counter mismatch: got %v want 1", got)
	}
	if got := testutil.ToFloat64(exp.metrics.scrapeSuccess); got != 0 {
		t.Fatalf("scrapeSuccess mismatch: got %v want 0", got)
	}
}

func TestRefreshMetricsWarningsCanBeZero(t *testing.T) {
	f := newMockRPCFixture()
	f.warnings = ""
	exp := newExporterForTest(t, f, false)

	if err := exp.refreshMetrics(context.Background()); err != nil {
		t.Fatalf("refreshMetrics failed: %v", err)
	}
	if got := testutil.ToFloat64(exp.metrics.warnings); got != 0 {
		t.Fatalf("bitcoin_warnings mismatch: got %v want 0", got)
	}
}
