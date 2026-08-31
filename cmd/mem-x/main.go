// Command mem-x runs the mem-x in-memory key/value server.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
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
	flag.IntVar(&cfg.MaxArgs, "max-args", cfg.MaxArgs, "max elements per command")
	flag.IntVar(&cfg.MaxInlineLen, "max-inline-len", cfg.MaxInlineLen, "max inline command length in bytes")
	flag.DurationVar(&cfg.IdleTimeout, "idle-timeout", cfg.IdleTimeout, "client idle timeout (0 = none)")
	flag.DurationVar(&cfg.TTLTick, "ttl-tick", cfg.TTLTick, "expiry sweeper interval")
	flag.IntVar(&cfg.Shards, "shards", cfg.Shards, "store shard count (0 = auto)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
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

	addr, err := srv.Listen(cfg.Addr)
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
