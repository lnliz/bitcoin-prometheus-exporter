package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var buildCommit = "unknown"

const satoshisPerCoin = 1e8

type metrics struct {
	blocks                    prometheus.Gauge
	headers                   prometheus.Gauge
	initialBlockDownload      prometheus.Gauge
	mediantime                prometheus.Gauge
	pruned                    prometheus.Gauge
	pruneHeight               prometheus.Gauge
	difficulty                prometheus.Gauge
	peers                     prometheus.Gauge
	connIn                    prometheus.Gauge
	connOut                   prometheus.Gauge
	relayFee                  prometheus.Gauge
	incrementalFee            prometheus.Gauge
	warnings                  prometheus.Gauge
	uptime                    prometheus.Gauge
	meminfoUsed               prometheus.Gauge
	meminfoFree               prometheus.Gauge
	meminfoTotal              prometheus.Gauge
	meminfoLocked             prometheus.Gauge
	meminfoChunksUsed         prometheus.Gauge
	meminfoChunksFree         prometheus.Gauge
	mempoolBytes              prometheus.Gauge
	mempoolSize               prometheus.Gauge
	mempoolUsage              prometheus.Gauge
	mempoolTotalFee           prometheus.Gauge
	mempoolMax                prometheus.Gauge
	mempoolMinfee             prometheus.Gauge
	mempoolFullRBF            prometheus.Gauge
	mempoolUnbroadcast        prometheus.Gauge
	latestBlockHeight         prometheus.Gauge
	latestBlockWeight         prometheus.Gauge
	latestBlockSize           prometheus.Gauge
	latestBlockTxs            prometheus.Gauge
	latestBlockInputs         prometheus.Gauge
	latestBlockOutputs        prometheus.Gauge
	latestBlockValue          prometheus.Gauge
	latestBlockFee            prometheus.Gauge
	latestBlockAvgFee         prometheus.Gauge
	latestBlockAvgFeeRate     prometheus.Gauge
	latestBlockMedianFee      prometheus.Gauge
	latestBlockUTXOIncrease   prometheus.Gauge
	latestBlockSWTxs          prometheus.Gauge
	latestBlockSWTotalSize    prometheus.Gauge
	latestBlockSWTotalWeight  prometheus.Gauge
	txcount                   prometheus.Gauge
	chaintipStatus            *prometheus.GaugeVec
	totalBytesRecv            prometheus.Gauge
	totalBytesSent            prometheus.Gauge
	bannedPeers               prometheus.Gauge
	banCreated                *prometheus.GaugeVec
	bannedUntil               *prometheus.GaugeVec
	serverVersion             prometheus.Gauge
	protocolVersion           prometheus.Gauge
	sizeOnDisk                prometheus.Gauge
	verificationProgress      prometheus.Gauge
	rpcActive                 prometheus.Gauge
	indexSynced               *prometheus.GaugeVec
	indexBestBlockHeight      *prometheus.GaugeVec
	scrapeSuccess             prometheus.Gauge
	exporterErrors            *prometheus.CounterVec
	exporterRPCErrorsByMethod *prometheus.CounterVec
	processTime               prometheus.Histogram

	hashpsGauges   map[int]prometheus.Gauge
	smartFeeGauges map[int]prometheus.Gauge

	registry *prometheus.Registry
}

func newMetrics(cfg config) *metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	m := &metrics{
		registry:       reg,
		hashpsGauges:   make(map[int]prometheus.Gauge),
		smartFeeGauges: make(map[int]prometheus.Gauge),
	}

	m.blocks = regGauge(reg, "bitcoin_blocks", "Block height")
	m.headers = regGauge(reg, "bitcoin_headers", "Latest block header height")
	m.initialBlockDownload = regGauge(reg, "bitcoin_initial_block_download", "Whether the node is in initial block download (1/0)")
	m.mediantime = regGauge(reg, "bitcoin_chain_tip_median_time", "Median time of the latest chain tip")
	m.pruned = regGauge(reg, "bitcoin_pruned", "Whether pruning is enabled (1/0)")
	m.pruneHeight = regGauge(reg, "bitcoin_prune_height", "Lowest-height block stored when pruning is enabled")
	m.difficulty = regGauge(reg, "bitcoin_difficulty", "Difficulty")
	m.peers = regGauge(reg, "bitcoin_peers", "Number of peers")
	m.connIn = regGauge(reg, "bitcoin_conn_in", "Number of connections in")
	m.connOut = regGauge(reg, "bitcoin_conn_out", "Number of connections out")
	m.relayFee = regGauge(reg, "bitcoin_relay_fee", "Minimum transaction relay fee in BTC/kB")
	m.incrementalFee = regGauge(reg, "bitcoin_incremental_fee", "Incremental relay fee in BTC/kB")
	m.warnings = regGauge(reg, "bitcoin_warnings", "Whether the node reports network or blockchain warnings (1/0)")
	m.uptime = regGauge(reg, "bitcoin_uptime", "Number of seconds the Bitcoin daemon has been running")

	m.meminfoUsed = regGauge(reg, "bitcoin_meminfo_used", "Number of bytes used")
	m.meminfoFree = regGauge(reg, "bitcoin_meminfo_free", "Number of bytes available")
	m.meminfoTotal = regGauge(reg, "bitcoin_meminfo_total", "Number of bytes managed")
	m.meminfoLocked = regGauge(reg, "bitcoin_meminfo_locked", "Number of bytes locked")
	m.meminfoChunksUsed = regGauge(reg, "bitcoin_meminfo_chunks_used", "Number of allocated chunks")
	m.meminfoChunksFree = regGauge(reg, "bitcoin_meminfo_chunks_free", "Number of unused chunks")

	m.mempoolBytes = regGauge(reg, "bitcoin_mempool_bytes", "Size of mempool in bytes")
	m.mempoolSize = regGauge(reg, "bitcoin_mempool_size", "Number of unconfirmed transactions in mempool")
	m.mempoolUsage = regGauge(reg, "bitcoin_mempool_usage_bytes", "Total memory usage for the mempool")
	m.mempoolTotalFee = regGauge(reg, "bitcoin_mempool_total_fee", "Total fees in mempool in BTC")
	m.mempoolMax = regGauge(reg, "bitcoin_mempool_max_bytes", "Maximum allowed mempool memory usage in bytes")
	m.mempoolMinfee = regGauge(reg, "bitcoin_mempool_minfee_btc_kvb", "Minimum fee rate in BTC/kB for tx to be accepted in mempool")
	m.mempoolFullRBF = regGauge(reg, "bitcoin_mempool_fullrbf", "Whether full-RBF is enabled for mempool policy (1/0)")
	m.mempoolUnbroadcast = regGauge(reg, "bitcoin_mempool_unbroadcast_transactions", "Number of transactions waiting for acknowledgment")

	m.latestBlockHeight = regGauge(reg, "bitcoin_latest_block_height", "Height or index of latest block")
	m.latestBlockWeight = regGauge(reg, "bitcoin_latest_block_weight", "Weight of latest block according to BIP 141")
	m.latestBlockSize = regGauge(reg, "bitcoin_latest_block_size_bytes", "Size of latest block in bytes")
	m.latestBlockTxs = regGauge(reg, "bitcoin_latest_block_txs", "Number of transactions in latest block")
	m.latestBlockInputs = regGauge(reg, "bitcoin_latest_block_inputs", "Number of inputs in transactions of latest block")
	m.latestBlockOutputs = regGauge(reg, "bitcoin_latest_block_outputs", "Number of outputs in transactions of latest block")
	m.latestBlockValue = regGauge(reg, "bitcoin_latest_block_value_btc", "Bitcoin value of all transactions in the latest block")
	m.latestBlockFee = regGauge(reg, "bitcoin_latest_block_fee_btc", "Total fee to process the latest block")
	m.latestBlockAvgFee = regGauge(reg, "bitcoin_latest_block_avg_fee_btc", "Average transaction fee in the latest block (BTC)")
	m.latestBlockAvgFeeRate = regGauge(reg, "bitcoin_latest_block_avg_feerate_sats_vb", "Average transaction fee rate in the latest block (sats/vb)")
	m.latestBlockMedianFee = regGauge(reg, "bitcoin_latest_block_median_fee_btc", "Median transaction fee in the latest block (BTC)")
	m.latestBlockUTXOIncrease = regGauge(reg, "bitcoin_latest_block_utxo_increase", "Net increase in the UTXO set caused by the latest block")
	m.latestBlockSWTxs = regGauge(reg, "bitcoin_latest_block_swtxs", "Number of segwit transactions in the latest block")
	m.latestBlockSWTotalSize = regGauge(reg, "bitcoin_latest_block_swtotal_size", "Total serialized size of segwit transactions in the latest block")
	m.latestBlockSWTotalWeight = regGauge(reg, "bitcoin_latest_block_swtotal_weight", "Total weight of segwit transactions in the latest block")

	m.txcount = regGauge(reg, "bitcoin_txcount", "Number of TX since the genesis block")
	if cfg.chaintips {
		m.chaintipStatus = regGaugeVec(reg, "bitcoin_chaintips", "Number of known blockchain branches by status", "status")
	}
	m.totalBytesRecv = regGauge(reg, "bitcoin_total_bytes_recv", "Total bytes received")
	m.totalBytesSent = regGauge(reg, "bitcoin_total_bytes_sent", "Total bytes sent")

	m.bannedPeers = regGauge(reg, "bitcoin_banned_peers", "Number of peers that have been banned")
	if cfg.banAddrMetrics {
		m.banCreated = regGaugeVec(reg, "bitcoin_ban_created", "Time the ban was created", "address", "reason")
		m.bannedUntil = regGaugeVec(reg, "bitcoin_banned_until", "Time the ban expires", "address", "reason")
	}
	m.serverVersion = regGauge(reg, "bitcoin_server_version", "The server version")
	m.protocolVersion = regGauge(reg, "bitcoin_protocol_version", "The protocol version of the server")
	m.sizeOnDisk = regGauge(reg, "bitcoin_size_on_disk", "Estimated size of the block and undo files")
	m.verificationProgress = regGauge(reg, "bitcoin_verification_progress", "Estimate of verification progress [0..1]")
	m.rpcActive = regGauge(reg, "bitcoin_rpc_active", "Number of RPC calls being processed")
	m.indexSynced = regGaugeVec(reg, "bitcoin_index_synced", "Whether a node index is synced (1/0)", "index")
	m.indexBestBlockHeight = regGaugeVec(reg, "bitcoin_index_best_block_height", "Best block height known by each node index", "index")
	m.scrapeSuccess = regGauge(reg, "bitcoin_exporter_scrape_success", "Whether the last exporter refresh completed without RPC errors (1/0)")

	m.exporterErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bitcoin_exporter_errors",
		Help: "Number of errors encountered by the exporter",
	}, []string{"type"})
	reg.MustRegister(m.exporterErrors)
	m.exporterRPCErrorsByMethod = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bitcoin_exporter_rpc_method_errors_total",
		Help: "Number of RPC call errors by method and error type",
	}, []string{"method", "type"})
	reg.MustRegister(m.exporterRPCErrorsByMethod)

	m.processTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "bitcoin_exporter_process_time_seconds",
		Help:    "Duration of each metrics refresh from the bitcoin node",
		Buckets: prometheus.DefBuckets,
	})
	reg.MustRegister(m.processTime)

	buildInfo := regGaugeVec(reg, "bitcoin_exporter_build_info", "Build information for the exporter", "goversion", "commit")
	buildInfo.WithLabelValues(runtime.Version(), buildCommit).Set(1)

	for _, n := range cfg.hashpsBlocks {
		name, desc := hashpsNameDesc(n)
		g := regGauge(reg, name, desc)
		m.hashpsGauges[n] = g
	}

	for _, n := range cfg.smartFeeBlocks {
		g := regGauge(reg, fmt.Sprintf("bitcoin_est_smart_fee_%d", n),
			fmt.Sprintf("Estimated smart fee per kilobyte for confirmation in %d blocks", n))
		m.smartFeeGauges[n] = g
	}

	return m
}

func regGauge(reg *prometheus.Registry, name, help string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
	reg.MustRegister(g)
	return g
}

func regGaugeVec(reg *prometheus.Registry, name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	reg.MustRegister(g)
	return g
}

func hashpsNameDesc(nblocks int) (name, desc string) {
	if nblocks < 0 {
		return fmt.Sprintf("bitcoin_hashps_neg%d", -nblocks),
			"Estimated network hash rate per second since the last difficulty change"
	}
	return fmt.Sprintf("bitcoin_hashps_%d", nblocks),
		fmt.Sprintf("Estimated network hash rate per second for the last %d blocks", nblocks)
}

type exporter struct {
	cfg              config
	rpc              *retryableRPC
	metrics          *metrics
	metricsHandler   http.Handler
	mu               sync.Mutex
	cachedBlockHash  string
	cachedBlockStats map[string]float64
}

func newExporter(cfg config) *exporter {
	var rpcURL, user, password string

	useConf := cfg.confPathSet || !cfg.userSet || !cfg.passwordSet

	if useConf {
		confPath := cfg.confPath
		if confPath == "" {
			defaultPath, err := defaultBitcoinConfPath()
			if err != nil {
				slog.Error("Failed to determine default config path", "error", err)
			} else {
				confPath = defaultPath
			}
		}
		if confPath != "" {
			slog.Info("Using config file", "path", confPath)

			confUser, confPass, confHost, confPort, err := parseConfFile(confPath)
			if err != nil {
				slog.Error("Failed to read config file", "path", confPath, "error", err)
			} else {
				user = confUser
				password = confPass
				host := cfg.rpcHost
				port := cfg.rpcPort
				if confHost != "" {
					host = confHost
				}
				if confPort != "" {
					port = confPort
				}
				rpcURL = fmt.Sprintf("%s://%s", cfg.rpcScheme, net.JoinHostPort(host, port))
			}
		} else {
			slog.Warn("No config path available; falling back to environment configuration")
		}
	}

	if rpcURL == "" {
		slog.Info("Using environment configuration")
		user = cfg.rpcUser
		password = cfg.rpcPassword
		host := cfg.rpcHost
		if cfg.rpcPort != "" {
			host = net.JoinHostPort(cfg.rpcHost, cfg.rpcPort)
		}
		rpcURL = fmt.Sprintf("%s://%s", cfg.rpcScheme, host)
	}

	client := newRPCClient(rpcURL, user, password, time.Duration(cfg.timeout)*time.Second)

	m := newMetrics(cfg)
	metricsHandler := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})

	rpc := &retryableRPC{
		client:       client,
		timeout:      time.Duration(cfg.timeout) * time.Second,
		maxBackoff:   30 * time.Second,
		initialDelay: 500 * time.Millisecond,
		onRetry: func(err error) {
			errType := errorTypeName(err)
			m.exporterErrors.WithLabelValues(errType).Inc()
			slog.Error("Retry after exception", "type", errType, "error", err)
		},
	}

	return &exporter{
		cfg:            cfg,
		rpc:            rpc,
		metrics:        m,
		metricsHandler: metricsHandler,
	}
}

func (e *exporter) rpcCallMap(ctx context.Context, method string, params ...any) (map[string]any, error) {
	raw, err := e.rpc.call(ctx, method, params...)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, &jsonDecodeError{cause: err, body: string(raw)}
	}
	return result, nil
}

func (e *exporter) rpcCallList(ctx context.Context, method string, params ...any) ([]any, error) {
	raw, err := e.rpc.call(ctx, method, params...)
	if err != nil {
		return nil, err
	}
	var result []any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, &jsonDecodeError{cause: err, body: string(raw)}
	}
	return result, nil
}

func (e *exporter) rpcCallFloat(ctx context.Context, method string, params ...any) (float64, error) {
	raw, err := e.rpc.call(ctx, method, params...)
	if err != nil {
		return 0, err
	}
	var result float64
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, &jsonDecodeError{cause: err, body: string(raw)}
	}
	return result, nil
}

func (e *exporter) getBlockStats(ctx context.Context, blockHash string) map[string]float64 {
	e.mu.Lock()
	if e.cachedBlockHash == blockHash && e.cachedBlockStats != nil {
		stats := e.cachedBlockStats
		e.mu.Unlock()
		return stats
	}
	e.mu.Unlock()

	fields := []string{
		"total_size", "total_weight", "totalfee", "txs", "height", "ins", "outs", "total_out",
		"avgfee", "avgfeerate", "medianfee", "utxo_increase", "swtxs", "swtotal_size", "swtotal_weight",
	}
	result, err := e.rpcCallMap(ctx, "getblockstats", blockHash, fields)
	if err != nil {
		slog.Error("Failed to retrieve block statistics from bitcoind", "block_hash", blockHash, "error", err)
		return nil
	}

	stats := make(map[string]float64)
	for _, f := range fields {
		stats[f] = jsonFloat(result[f])
	}

	e.mu.Lock()
	e.cachedBlockHash = blockHash
	e.cachedBlockStats = stats
	e.mu.Unlock()

	return stats
}

func (e *exporter) refreshMetrics(ctx context.Context) error {
	m := e.metrics
	var refreshErrs []error
	recordRPCError := func(method string, err error) {
		if err == nil {
			return
		}
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			slog.Debug("RPC method not available on this node", "method", method)
			return
		}
		errType := exporterErrorType(err)
		m.exporterErrors.WithLabelValues(errType).Inc()
		m.exporterRPCErrorsByMethod.WithLabelValues(method, errType).Inc()
		refreshErrs = append(refreshErrs, fmt.Errorf("%s: %w", method, err))
		slog.Error("RPC method failed during refresh", "method", method, "type", errType, "error", err)
	}

	callMap := func(method string, params ...any) (map[string]any, bool) {
		result, err := e.rpcCallMap(ctx, method, params...)
		if err != nil {
			recordRPCError(method, err)
			return nil, false
		}
		return result, true
	}
	callList := func(method string, params ...any) ([]any, bool) {
		result, err := e.rpcCallList(ctx, method, params...)
		if err != nil {
			recordRPCError(method, err)
			return nil, false
		}
		return result, true
	}
	callFloat := func(method string, params ...any) (float64, bool) {
		result, err := e.rpcCallFloat(ctx, method, params...)
		if err != nil {
			recordRPCError(method, err)
			return 0, false
		}
		return result, true
	}

	var blockchaininfo map[string]any
	var bestBlockHash string

	if uptime, ok := callFloat("uptime"); ok {
		m.uptime.Set(math.Trunc(uptime))
	}

	if memoryinfo, ok := callMap("getmemoryinfo", "stats"); ok {
		if meminfo, ok := memoryinfo["locked"].(map[string]any); ok {
			m.meminfoUsed.Set(jsonFloat(meminfo["used"]))
			m.meminfoFree.Set(jsonFloat(meminfo["free"]))
			m.meminfoTotal.Set(jsonFloat(meminfo["total"]))
			m.meminfoLocked.Set(jsonFloat(meminfo["locked"]))
			m.meminfoChunksUsed.Set(jsonFloat(meminfo["chunks_used"]))
			m.meminfoChunksFree.Set(jsonFloat(meminfo["chunks_free"]))
		}
	}

	if bci, ok := callMap("getblockchaininfo"); ok {
		blockchaininfo = bci
		bestBlockHash, _ = blockchaininfo["bestblockhash"].(string)
		m.blocks.Set(jsonFloat(blockchaininfo["blocks"]))
		m.headers.Set(jsonFloat(blockchaininfo["headers"]))
		m.initialBlockDownload.Set(jsonBool(blockchaininfo["initialblockdownload"]))
		m.mediantime.Set(jsonFloat(blockchaininfo["mediantime"]))
		m.pruned.Set(jsonBool(blockchaininfo["pruned"]))
		if pruned, _ := blockchaininfo["pruned"].(bool); pruned {
			m.pruneHeight.Set(jsonFloat(blockchaininfo["pruneheight"]))
		}
		m.difficulty.Set(jsonFloat(blockchaininfo["difficulty"]))
		m.sizeOnDisk.Set(jsonFloat(blockchaininfo["size_on_disk"]))
		m.verificationProgress.Set(jsonFloat(blockchaininfo["verificationprogress"]))
	}

	if networkinfo, ok := callMap("getnetworkinfo"); ok {
		m.peers.Set(jsonFloat(networkinfo["connections"]))
		if _, ok := networkinfo["connections_in"]; ok {
			m.connIn.Set(jsonFloat(networkinfo["connections_in"]))
		}
		if _, ok := networkinfo["connections_out"]; ok {
			m.connOut.Set(jsonFloat(networkinfo["connections_out"]))
		}
		m.relayFee.Set(jsonFloat(networkinfo["relayfee"]))
		m.incrementalFee.Set(jsonFloat(networkinfo["incrementalfee"]))
		m.serverVersion.Set(jsonFloat(networkinfo["version"]))
		m.protocolVersion.Set(jsonFloat(networkinfo["protocolversion"]))
		warnings, _ := networkinfo["warnings"].(string)
		if warnings != "" {
			m.warnings.Set(1)
		} else {
			m.warnings.Set(0)
		}
	}

	if e.cfg.chaintips {
		if chaintips, ok := callList("getchaintips"); ok {
			if m.chaintipStatus != nil {
				m.chaintipStatus.Reset()
				statusCounts := map[string]float64{}
				for _, tipRaw := range chaintips {
					tip, ok := tipRaw.(map[string]any)
					if !ok {
						continue
					}
					status, _ := tip["status"].(string)
					if status == "" {
						status = "unknown"
					}
					statusCounts[status]++
				}
				for status, count := range statusCounts {
					m.chaintipStatus.WithLabelValues(status).Set(count)
				}
			}
		}
	}

	if mempool, ok := callMap("getmempoolinfo"); ok {
		m.mempoolBytes.Set(jsonFloat(mempool["bytes"]))
		m.mempoolSize.Set(jsonFloat(mempool["size"]))
		m.mempoolUsage.Set(jsonFloat(mempool["usage"]))
		m.mempoolTotalFee.Set(jsonFloat(mempool["total_fee"]))
		m.mempoolMax.Set(jsonFloat(mempool["maxmempool"]))
		m.mempoolMinfee.Set(jsonFloat(mempool["mempoolminfee"]))
		m.mempoolFullRBF.Set(jsonBool(mempool["fullrbf"]))
		if _, ok := mempool["unbroadcastcount"]; ok {
			m.mempoolUnbroadcast.Set(jsonFloat(mempool["unbroadcastcount"]))
		}
	}

	if nettotals, ok := callMap("getnettotals"); ok {
		m.totalBytesRecv.Set(jsonFloat(nettotals["totalbytesrecv"]))
		m.totalBytesSent.Set(jsonFloat(nettotals["totalbytessent"]))
	}

	if rpcinfo, ok := callMap("getrpcinfo"); ok {
		activeCommands, _ := rpcinfo["active_commands"].([]any)
		m.rpcActive.Set(float64(len(activeCommands)))
	}

	if indexInfo, ok := callMap("getindexinfo"); ok {
		m.indexSynced.Reset()
		m.indexBestBlockHeight.Reset()
		for name, raw := range indexInfo {
			info, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			m.indexSynced.WithLabelValues(name).Set(jsonBool(info["synced"]))
			best, _ := info["best_block_height"]
			m.indexBestBlockHeight.WithLabelValues(name).Set(jsonFloat(best))
		}
	}

	if txstats, ok := callMap("getchaintxstats"); ok {
		m.txcount.Set(jsonFloat(txstats["txcount"]))
	}

	if bestBlockHash != "" {
		latestBlockStats := e.getBlockStats(ctx, bestBlockHash)
		if latestBlockStats != nil {
			m.latestBlockSize.Set(latestBlockStats["total_size"])
			m.latestBlockTxs.Set(latestBlockStats["txs"])
			m.latestBlockHeight.Set(latestBlockStats["height"])
			m.latestBlockWeight.Set(latestBlockStats["total_weight"])
			m.latestBlockInputs.Set(latestBlockStats["ins"])
			m.latestBlockOutputs.Set(latestBlockStats["outs"])
			m.latestBlockValue.Set(latestBlockStats["total_out"] / satoshisPerCoin)
			m.latestBlockFee.Set(latestBlockStats["totalfee"] / satoshisPerCoin)
			m.latestBlockAvgFee.Set(latestBlockStats["avgfee"] / satoshisPerCoin)
			m.latestBlockAvgFeeRate.Set(latestBlockStats["avgfeerate"])
			m.latestBlockMedianFee.Set(latestBlockStats["medianfee"] / satoshisPerCoin)
			m.latestBlockUTXOIncrease.Set(latestBlockStats["utxo_increase"])
			m.latestBlockSWTxs.Set(latestBlockStats["swtxs"])
			m.latestBlockSWTotalSize.Set(latestBlockStats["swtotal_size"])
			m.latestBlockSWTotalWeight.Set(latestBlockStats["swtotal_weight"])
		}
	}

	if banned, ok := callList("listbanned"); ok {
		m.bannedPeers.Set(float64(len(banned)))
		if e.cfg.banAddrMetrics && m.banCreated != nil && m.bannedUntil != nil {
			m.banCreated.Reset()
			m.bannedUntil.Reset()
			limit := e.cfg.banAddrLimit
			if limit <= 0 || limit > len(banned) {
				limit = len(banned)
			} else if len(banned) > limit {
				slog.Debug("Truncating banned peer label metrics", "limit", limit, "total", len(banned))
			}
			for i := 0; i < limit; i++ {
				ban, ok := banned[i].(map[string]any)
				if !ok {
					continue
				}
				addr, _ := ban["address"].(string)
				reason, _ := ban["ban_reason"].(string)
				if reason == "" {
					reason = "manually added"
				}
				m.banCreated.WithLabelValues(addr, reason).Set(jsonFloat(ban["ban_created"]))
				m.bannedUntil.WithLabelValues(addr, reason).Set(jsonFloat(ban["banned_until"]))
			}
		}
	}

	for _, n := range e.cfg.smartFeeBlocks {
		result, err := e.rpcCallMap(ctx, "estimatesmartfee", n)
		if err != nil {
			recordRPCError("estimatesmartfee", err)
			continue
		}
		if feerate, ok := result["feerate"]; ok && feerate != nil {
			if g, exists := m.smartFeeGauges[n]; exists {
				g.Set(jsonFloat(feerate))
			}
		}
	}

	for _, n := range e.cfg.hashpsBlocks {
		hps, err := e.rpcCallFloat(ctx, "getnetworkhashps", n)
		if err != nil {
			recordRPCError("getnetworkhashps", err)
			continue
		}
		if g, exists := m.hashpsGauges[n]; exists {
			g.Set(hps)
		}
	}

	if len(refreshErrs) > 0 {
		return errors.Join(refreshErrs...)
	}
	return nil
}

func (e *exporter) handleMetrics(w http.ResponseWriter, r *http.Request) {
	processStart := time.Now()

	err := e.refreshMetrics(r.Context())
	if err != nil {
		e.metrics.scrapeSuccess.Set(0)
		slog.Error("Refresh completed with one or more RPC errors", "error", err)
	} else {
		e.metrics.scrapeSuccess.Set(1)
	}

	duration := time.Since(processStart)
	e.metrics.processTime.Observe(duration.Seconds())
	slog.Debug("Refresh took", "duration", duration)

	e.metricsHandler.ServeHTTP(w, r)
}

func exporterErrorType(err error) string {
	var decodeErr *jsonDecodeError
	if errors.As(err, &decodeErr) {
		return "json_decode"
	}
	if strings.Contains(err.Error(), "retry timeout") {
		return "retry_timeout"
	}
	return errorTypeName(err)
}
