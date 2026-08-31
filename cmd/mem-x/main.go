// Command mem-x runs the mem-x in-memory key/value server.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"mem-x/internal/command"
	"mem-x/internal/config"
	"mem-x/internal/server"
	"mem-x/internal/store"
	"mem-x/internal/version"
)

func main() {
	cfg := config.Default()
	flag.StringVar(&cfg.Addr, "addr", cfg.Addr, "TCP listen address")
	flag.IntVar(&cfg.MaxConn, "max-conn", cfg.MaxConn, "max concurrent client connections")
	flag.IntVar(&cfg.MaxBulkLen, "max-bulk-len", cfg.MaxBulkLen, "max bulk string length in bytes")
	flag.Int64Var(&cfg.MaxValueLen, "max-value-len", cfg.MaxValueLen, "max stored value size in bytes")
	flag.IntVar(&cfg.MaxArgs, "max-args", cfg.MaxArgs, "max elements per command")
	flag.IntVar(&cfg.MaxInlineLen, "max-inline-len", cfg.MaxInlineLen, "max inline command length in bytes")
	flag.DurationVar(&cfg.IdleTimeout, "idle-timeout", cfg.IdleTimeout, "client idle timeout (0 = none)")
	flag.DurationVar(&cfg.TTLTick, "ttl-tick", cfg.TTLTick, "expiry sweeper interval")
	flag.IntVar(&cfg.Shards, "shards", cfg.Shards, "store shard count (0 = auto)")
	logLevel := flag.String("log-level", "info", "log level: debug|info|warn|error (suppress less-important logs)")
	flag.Parse()

	lvl, err := parseLogLevel(*logLevel)
	if err != nil {
		// Treat an invalid level as an error rather than silently defaulting,
		// so operators catch typos in startup scripts.
		slog.Error("invalid log level", "level", *logLevel, "err", err)
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
	slog.Info("mem-x starting", "version", version.Version, "go", runtime.Version())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st := store.New(store.WithShards(cfg.Shards), store.WithMaxValueLen(cfg.MaxValueLen))
	stats := command.NewStats()
	reg := command.New(st, stats)
	srv := server.New(cfg, st, reg, stats)

	expCtx, cancelExp := context.WithCancel(ctx)
	defer cancelExp()
	st.StartExpiry(expCtx, cfg.TTLTick)

	addr, err := listenWithRetry(srv, cfg.Addr, 10)
	if err != nil {
		slog.Error("listen failed", "err", err)
		os.Exit(1)
	}
	slog.Info("listening", "addr", addr.String(), "shards", st.ShardCount())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining in-flight commands")
	case err := <-errCh:
		if err != nil {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}
	if err := <-errCh; err != nil {
		slog.Error("shutdown error", "err", err)
		os.Exit(1)
	}
	slog.Info("mem-x stopped")
}

// parseLogLevel maps a CLI string to a slog.Level. Accepts the standard
// names (debug, info, warn, error) case-insensitively.
func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unknown level %q (want debug|info|warn|error)", s)
	}
}

// listenWithRetry binds the server, and if the requested port is already in
// use, transparently retries on the next port(s) so a busy default never
// blocks startup. Each successful reassignment is logged at WARN so
// operators notice the deviation. addr is "host:port" (or ":port").
func listenWithRetry(srv *server.Server, addr string, maxRetries int) (net.Addr, error) {
	addr2, err := tryListen(srv, addr)
	if err == nil || maxRetries <= 0 {
		return addr2, err
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err // malformed addr: report it, do not retry
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	for i := 0; i < maxRetries; i++ {
		next := fmt.Sprintf("%s:%d", host, port+i+1)
		addr2, err := tryListen(srv, next)
		if err == nil {
			slog.Warn("port in use, reassigned", "requested", addr, "listening", addr2.String())
			return addr2, nil
		}
	}
	return nil, err
}

// tryListen binds srv to addr and returns the bound address.
func tryListen(srv *server.Server, addr string) (net.Addr, error) {
	return srv.Listen(addr)
}
