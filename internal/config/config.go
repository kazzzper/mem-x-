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
	// AOFPath enables append-only persistence when non-empty. When set, every
	// write command is appended to this file and the store is rebuilt from it
	// on startup.
	AOFPath string
	// AppendFsync is the fsync policy: "always", "everysec" (default), or
	// "no". Only consulted when AOFPath is non-empty.
	AppendFsync string
	// TLSCertFile and TLSKeyFile enable native TLS when both are set: the
	// server wraps its listener with tls.NewListener using the PEM cert chain
	// and private key. Empty (the default) keeps the listener plaintext.
	// Clients connect with memxs:// URLs.
	TLSCertFile string
	TLSKeyFile  string
	// RequirePass, when non-empty, enables authentication: every connection
	// must issue AUTH <password> before other commands are accepted. This is
	// the password in a memx://user:pass@host connection URL.
	RequirePass string
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
