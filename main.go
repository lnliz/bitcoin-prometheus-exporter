package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	rpcScheme   string
	rpcHost     string
	rpcPort     string
	rpcUser     string
	rpcPassword string
	confPath    string
	confPathSet bool
	userSet     bool
	passwordSet bool

	hashpsBlocks   []int
	smartFeeBlocks []int

	metricsAddr    string
	metricsPort    int
	timeout        int
	logLevel       string
	banAddrMetrics bool
	banAddrLimit   int
}

func loadConfig() config {
	cfg := config{
		rpcScheme:   envOrDefault("BITCOIN_RPC_SCHEME", "http"),
		rpcHost:     envOrDefault("BITCOIN_RPC_HOST", "localhost"),
		rpcPort:     envOrDefault("BITCOIN_RPC_PORT", "8332"),
		metricsAddr: envOrDefault("METRICS_ADDR", ""),
		logLevel:    envOrDefault("LOG_LEVEL", "INFO"),
	}

	cfg.rpcUser, cfg.userSet = os.LookupEnv("BITCOIN_RPC_USER")
	cfg.rpcPassword, cfg.passwordSet = os.LookupEnv("BITCOIN_RPC_PASSWORD")
	cfg.confPath, cfg.confPathSet = os.LookupEnv("BITCOIN_CONF_PATH")

	cfg.metricsPort = envIntOrDefault("METRICS_PORT", 9332)
	cfg.timeout = envIntOrDefault("TIMEOUT", 30)
	cfg.banAddrMetrics = strings.EqualFold(envOrDefault("BAN_ADDRESS_METRICS", "false"), "true")
	cfg.banAddrLimit = envIntOrDefault("BAN_ADDRESS_LIMIT", 100)

	cfg.hashpsBlocks = parseIntList(envOrDefault("HASHPS_BLOCKS", "-1,1,120"))
	cfg.smartFeeBlocks = parseIntList(envOrDefault("SMARTFEE_BLOCKS", "2,3,5,20"))

	return cfg
}

func envOrDefault(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	s := os.Getenv(key)
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}

func parseIntList(s string) []int {
	var result []int
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			slog.Warn("Ignoring invalid integer in list", "value", part, "error", err)
			continue
		}
		result = append(result, v)
	}
	return result
}

func defaultBitcoinConfPath() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "Bitcoin", "bitcoin.conf"), nil
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(appdata, "Bitcoin", "bitcoin.conf"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".bitcoin", "bitcoin.conf"), nil
	}
}

func parseConfFile(path string) (user, password, host, port string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", "", err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "rpcuser":
			user = v
		case "rpcpassword":
			password = v
		case "rpcconnect":
			host = v
		case "rpcport":
			port = v
		}
	}
	return
}

func parseSlogLevel(s string) slog.Level {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "ERROR", "CRITICAL":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

const rootPageHTML = `<!DOCTYPE html>
<html>
<head><title>bitcoin prometheus exporter</title></head>
<body>
<h1>bitcoin prometheus exporter</h1>
<p><a href="/metrics">metrics</a></p>
</body>
</html>
`

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, rootPageHTML)
}

func main() {
	cfg := loadConfig()

	level := parseSlogLevel(cfg.logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().UTC().Format("2006-01-02T15:04:05Z"))
			}
			return a
		},
	}))
	slog.SetDefault(logger)

	exp := newExporter(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/metrics", exp.handleMetrics)

	addr := fmt.Sprintf("%s:%d", cfg.metricsAddr, cfg.metricsPort)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigCh
		slog.Info(fmt.Sprintf("Received %s. Shutting down.", sig))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			slog.Error("Shutdown error", "error", err)
		}
	}()

	slog.Info("Starting server", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("Server failed", "error", err)
		return
	}
}
