// Package config holds the runtime configuration for a mem-x server. Values
// are explicit — no globals, per AGENTS.md §4 — and a Config is built in
// cmd/mem-x and passed to the server.
package config

import "time"

// Config is the runtime configuration for one mem-x server.
type Config struct {
	Addr         string
	MaxConn      int
	MaxBulkLen   int
	MaxArgs      int
	MaxInlineLen int
	MaxValueLen  int64
	IdleTimeout  time.Duration
	TTLTick      time.Duration
	Shards       int // 0 = auto
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Addr:         ":6379",
		MaxConn:      10000,
		MaxBulkLen:   64 << 20, // 64 MiB
		MaxArgs:      1024 * 1024,
		MaxInlineLen: 64 << 10,  // 64 KiB
		MaxValueLen:  512 << 20, // 512 MiB (Redis proto-max-bulk-len default)
		IdleTimeout:  0,         // no idle timeout
		TTLTick:      100 * time.Millisecond,
		Shards:       0,
	}
}
