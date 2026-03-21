package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const satoshisPerCoin = 1e8

type metrics struct {
	blocks               prometheus.Gauge
	difficulty           prometheus.Gauge
	peers                prometheus.Gauge
	connIn               prometheus.Gauge
	connOut              prometheus.Gauge
	warnings             prometheus.Gauge
	uptime               prometheus.Gauge
	meminfoUsed          prometheus.Gauge
	meminfoFree          prometheus.Gauge
	meminfoTotal         prometheus.Gauge
	meminfoLocked        prometheus.Gauge
	meminfoChunksUsed    prometheus.Gauge
	meminfoChunksFree    prometheus.Gauge
	mempoolBytes         prometheus.Gauge
	mempoolSize          prometheus.Gauge
	mempoolUsage         prometheus.Gauge
	mempoolMinfee        prometheus.Gauge
	mempoolUnbroadcast   prometheus.Gauge
	latestBlockHeight    prometheus.Gauge
	latestBlockWeight    prometheus.Gauge
	latestBlockSize      prometheus.Gauge
	latestBlockTxs       prometheus.Gauge
	latestBlockInputs    prometheus.Gauge
	latestBlockOutputs   prometheus.Gauge
	latestBlockValue     prometheus.Gauge
	latestBlockFee       prometheus.Gauge
	txcount              prometheus.Gauge
	numChaintips         prometheus.Gauge
	totalBytesRecv       prometheus.Gauge
	totalBytesSent       prometheus.Gauge
	bannedPeers          prometheus.Gauge
	banCreated           *prometheus.GaugeVec
	bannedUntil          *prometheus.GaugeVec
	serverVersion        prometheus.Gauge
	protocolVersion      prometheus.Gauge
	sizeOnDisk           prometheus.Gauge
	verificationProgress prometheus.Gauge
	rpcActive            prometheus.Gauge
	exporterErrors       *prometheus.CounterVec
	processTime          prometheus.Histogram

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
	m.difficulty = regGauge(reg, "bitcoin_difficulty", "Difficulty")
	m.peers = regGauge(reg, "bitcoin_peers", "Number of peers")
	m.connIn = regGauge(reg, "bitcoin_conn_in", "Number of connections in")
	m.connOut = regGauge(reg, "bitcoin_conn_out", "Number of connections out")
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
	m.mempoolUsage = regGauge(reg, "bitcoin_mempool_usage", "Total memory usage for the mempool")
	m.mempoolMinfee = regGauge(reg, "bitcoin_mempool_minfee", "Minimum fee rate in BTC/kB for tx to be accepted in mempool")
	m.mempoolUnbroadcast = regGauge(reg, "bitcoin_mempool_unbroadcast", "Number of transactions waiting for acknowledgment")

	m.latestBlockHeight = regGauge(reg, "bitcoin_latest_block_height", "Height or index of latest block")
	m.latestBlockWeight = regGauge(reg, "bitcoin_latest_block_weight", "Weight of latest block according to BIP 141")
	m.latestBlockSize = regGauge(reg, "bitcoin_latest_block_size", "Size of latest block in bytes")
	m.latestBlockTxs = regGauge(reg, "bitcoin_latest_block_txs", "Number of transactions in latest block")
	m.latestBlockInputs = regGauge(reg, "bitcoin_latest_block_inputs", "Number of inputs in transactions of latest block")
	m.latestBlockOutputs = regGauge(reg, "bitcoin_latest_block_outputs", "Number of outputs in transactions of latest block")
	m.latestBlockValue = regGauge(reg, "bitcoin_latest_block_value", "Bitcoin value of all transactions in the latest block")
	m.latestBlockFee = regGauge(reg, "bitcoin_latest_block_fee", "Total fee to process the latest block")

	m.txcount = regGauge(reg, "bitcoin_txcount", "Number of TX since the genesis block")
	if cfg.chaintips {
		m.numChaintips = regGauge(reg, "bitcoin_num_chaintips", "Number of known blockchain branches")
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

	m.exporterErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "bitcoin_exporter_errors",
		Help: "Number of errors encountered by the exporter",
	}, []string{"type"})
	reg.MustRegister(m.exporterErrors)

	m.processTime = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "bitcoin_exporter_process_time_seconds",
		Help:    "Duration of each metrics refresh from the bitcoin node",
		Buckets: prometheus.DefBuckets,
	})
	reg.MustRegister(m.processTime)

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

func regCounter(reg *prometheus.Registry, name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	reg.MustRegister(c)
	return c
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

func (e *exporter) rpcCallMap(method string, params ...any) (map[string]any, error) {
	raw, err := e.rpc.call(method, params...)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, &jsonDecodeError{cause: err, body: string(raw)}
	}
	return result, nil
}

func (e *exporter) rpcCallList(method string, params ...any) ([]any, error) {
	raw, err := e.rpc.call(method, params...)
	if err != nil {
		return nil, err
	}
	var result []any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, &jsonDecodeError{cause: err, body: string(raw)}
	}
	return result, nil
}

func (e *exporter) rpcCallFloat(method string, params ...any) (float64, error) {
	raw, err := e.rpc.call(method, params...)
	if err != nil {
		return 0, err
	}
	var result float64
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, &jsonDecodeError{cause: err, body: string(raw)}
	}
	return result, nil
}

func (e *exporter) getBlockStats(blockHash string) map[string]float64 {
	e.mu.Lock()
	if e.cachedBlockHash == blockHash && e.cachedBlockStats != nil {
		stats := e.cachedBlockStats
		e.mu.Unlock()
		return stats
	}
	e.mu.Unlock()

	fields := []string{"total_size", "total_weight", "totalfee", "txs", "height", "ins", "outs", "total_out"}
	result, err := e.rpcCallMap("getblockstats", blockHash, fields)
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

func (e *exporter) refreshMetrics() error {
	m := e.metrics

	uptime, err := e.rpcCallFloat("uptime")
	if err != nil {
		return err
	}
	m.uptime.Set(math.Trunc(uptime))

	memoryinfo, err := e.rpcCallMap("getmemoryinfo", "stats")
	if err != nil {
		return err
	}
	if meminfo, ok := memoryinfo["locked"].(map[string]any); ok {
		m.meminfoUsed.Set(jsonFloat(meminfo["used"]))
		m.meminfoFree.Set(jsonFloat(meminfo["free"]))
		m.meminfoTotal.Set(jsonFloat(meminfo["total"]))
		m.meminfoLocked.Set(jsonFloat(meminfo["locked"]))
		m.meminfoChunksUsed.Set(jsonFloat(meminfo["chunks_used"]))
		m.meminfoChunksFree.Set(jsonFloat(meminfo["chunks_free"]))
	}

	blockchaininfo, err := e.rpcCallMap("getblockchaininfo")
	if err != nil {
		return err
	}
	m.blocks.Set(jsonFloat(blockchaininfo["blocks"]))
	m.difficulty.Set(jsonFloat(blockchaininfo["difficulty"]))
	m.sizeOnDisk.Set(jsonFloat(blockchaininfo["size_on_disk"]))
	m.verificationProgress.Set(jsonFloat(blockchaininfo["verificationprogress"]))

	networkinfo, err := e.rpcCallMap("getnetworkinfo")
	if err != nil {
		return err
	}
	m.peers.Set(jsonFloat(networkinfo["connections"]))
	if _, ok := networkinfo["connections_in"]; ok {
		m.connIn.Set(jsonFloat(networkinfo["connections_in"]))
	}
	if _, ok := networkinfo["connections_out"]; ok {
		m.connOut.Set(jsonFloat(networkinfo["connections_out"]))
	}
	m.serverVersion.Set(jsonFloat(networkinfo["version"]))
	m.protocolVersion.Set(jsonFloat(networkinfo["protocolversion"]))
	warnings, _ := networkinfo["warnings"].(string)
	if warnings != "" {
		m.warnings.Set(1)
	} else {
		m.warnings.Set(0)
	}

	if e.cfg.chaintips {
		chaintips, err := e.rpcCallList("getchaintips")
		if err != nil {
			return err
		}
		m.numChaintips.Set(float64(len(chaintips)))
	}

	mempool, err := e.rpcCallMap("getmempoolinfo")
	if err != nil {
		return err
	}
	m.mempoolBytes.Set(jsonFloat(mempool["bytes"]))
	m.mempoolSize.Set(jsonFloat(mempool["size"]))
	m.mempoolUsage.Set(jsonFloat(mempool["usage"]))
	m.mempoolMinfee.Set(jsonFloat(mempool["mempoolminfee"]))
	if _, ok := mempool["unbroadcastcount"]; ok {
		m.mempoolUnbroadcast.Set(jsonFloat(mempool["unbroadcastcount"]))
	}

	nettotals, err := e.rpcCallMap("getnettotals")
	if err != nil {
		return err
	}
	m.totalBytesRecv.Set(jsonFloat(nettotals["totalbytesrecv"]))
	m.totalBytesSent.Set(jsonFloat(nettotals["totalbytessent"]))

	rpcinfo, err := e.rpcCallMap("getrpcinfo")
	if err != nil {
		return err
	}
	activeCommands, _ := rpcinfo["active_commands"].([]any)
	m.rpcActive.Set(float64(len(activeCommands)))

	txstats, err := e.rpcCallMap("getchaintxstats")
	if err != nil {
		return err
	}
	m.txcount.Set(jsonFloat(txstats["txcount"]))

	bestBlockHash, _ := blockchaininfo["bestblockhash"].(string)
	latestBlockStats := e.getBlockStats(bestBlockHash)
	if latestBlockStats != nil {
		m.latestBlockSize.Set(latestBlockStats["total_size"])
		m.latestBlockTxs.Set(latestBlockStats["txs"])
		m.latestBlockHeight.Set(latestBlockStats["height"])
		m.latestBlockWeight.Set(latestBlockStats["total_weight"])
		m.latestBlockInputs.Set(latestBlockStats["ins"])
		m.latestBlockOutputs.Set(latestBlockStats["outs"])
		m.latestBlockValue.Set(latestBlockStats["total_out"] / satoshisPerCoin)
		m.latestBlockFee.Set(latestBlockStats["totalfee"] / satoshisPerCoin)
	}

	banned, err := e.rpcCallList("listbanned")
	if err != nil {
		return err
	}
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

	for _, n := range e.cfg.smartFeeBlocks {
		result, err := e.rpcCallMap("estimatesmartfee", n)
		if err != nil {
			slog.Error("estimatesmartfee failed", "blocks", n, "error", err)
			continue
		}
		if feerate, ok := result["feerate"]; ok && feerate != nil {
			if g, exists := m.smartFeeGauges[n]; exists {
				g.Set(jsonFloat(feerate))
			}
		}
	}

	for _, n := range e.cfg.hashpsBlocks {
		hps, err := e.rpcCallFloat("getnetworkhashps", n)
		if err != nil {
			slog.Error("getnetworkhashps failed", "blocks", n, "error", err)
			continue
		}
		if g, exists := m.hashpsGauges[n]; exists {
			g.Set(hps)
		}
	}

	return nil
}

func (e *exporter) handleMetrics(w http.ResponseWriter, r *http.Request) {
	processStart := time.Now()

	err := e.refreshMetrics()
	if err != nil {
		switch err.(type) {
		case *jsonDecodeError:
			slog.Error("RPC call did not return JSON. Bad credentials?", "error", err)
			e.metrics.exporterErrors.WithLabelValues("json_decode").Inc()
		case *rpcError:
			slog.Error("Bitcoin RPC error during refresh", "error", err)
			e.metrics.exporterErrors.WithLabelValues(errorTypeName(err)).Inc()
		default:
			if strings.Contains(err.Error(), "retry timeout") {
				slog.Error("Refresh failed during retry", "error", err)
				e.metrics.exporterErrors.WithLabelValues("retry_timeout").Inc()
			} else {
				slog.Error("Bitcoin RPC error during refresh", "error", err)
				e.metrics.exporterErrors.WithLabelValues(errorTypeName(err)).Inc()
			}
		}
	}

	duration := time.Since(processStart)
	e.metrics.processTime.Observe(duration.Seconds())
	slog.Debug("Refresh took", "duration", duration)

	e.metricsHandler.ServeHTTP(w, r)
}
