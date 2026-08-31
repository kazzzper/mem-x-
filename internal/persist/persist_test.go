package persist_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mem-x/internal/command"
	"mem-x/internal/persist"
	"mem-x/internal/store"
)

func TestParseFsyncPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  persist.FsyncPolicy
	}{
		{"always", persist.FsyncAlways},
		{"ALWAYS", persist.FsyncAlways},
		{"everysec", persist.FsyncEverysec},
		{"EVERYSEC", persist.FsyncEverysec},
		{"no", persist.FsyncNo},
		{"NO", persist.FsyncNo},
		{"", persist.FsyncEverysec},      // default
		{"bogus", persist.FsyncEverysec}, // unknown → default
	}
	for _, tt := range tests {
		got := persist.ParseFsyncPolicy(tt.input)
		if got != tt.want {
			t.Errorf("ParseFsyncPolicy(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	st := store.New()
	reg := command.New(st, command.NewStats())
	n, err := persist.Load("/tmp/mem-x-nonexistent.aof", reg)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 loaded commands, got %d", n)
	}
}

func TestAppendLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.aof")

	// Phase 1: open, write, verify the propagator interface works.
	aof, err := persist.Open(path, persist.FsyncAlways)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(store.WithShards(4))
	reg := command.New(st, command.NewStats())
	reg.SetPropagator(func(args [][]byte) {
		if err := aof.Append(args); err != nil {
			t.Errorf("AOF append failed: %v", err)
		}
	})

	ctx := context.Background()

	// SET key1 value1
	reg.Execute(ctx, tokenize("set", "key1", "value1"))
	// SET key2 value2
	reg.Execute(ctx, tokenize("set", "key2", "value2"))
	// INCRBY counter 5
	reg.Execute(ctx, tokenize("incrby", "counter", "5"))
	// APPEND key1 -suffix
	reg.Execute(ctx, tokenize("append", "key1", "-suffix"))

	if err := aof.Close(); err != nil {
		t.Fatal(err)
	}

	// Phase 2: load into a fresh store + registry (no propagator).
	st2 := store.New(store.WithShards(4))
	reg2 := command.New(st2, command.NewStats())
	n, err := persist.Load(path, reg2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("expected 4 commands, got %d", n)
	}

	// Verify the loaded state.
	expect := func(key, want string) {
		t.Helper()
		raw, ok := st2.Get([]byte(key))
		if !ok {
			t.Errorf("key %q not found", key)
			return
		}
		if string(raw) != want {
			t.Errorf("key %q = %q, want %q", key, string(raw), want)
		}
	}
	expect("key1", "value1-suffix")
	expect("key2", "value2")
	expect("counter", "5")
}

func TestAppendLoadWithTTL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ttl.aof")

	aof, err := persist.Open(path, persist.FsyncAlways)
	if err != nil {
		t.Fatal(err)
	}

	st := store.New(store.WithShards(4))
	reg := command.New(st, command.NewStats())
	reg.SetPropagator(func(args [][]byte) {
		if err := aof.Append(args); err != nil {
			t.Errorf("AOF append failed: %v", err)
		}
	})

	ctx := context.Background()
	// SET with EX (relative TTL) — should be rewritten to PEXPIREAT in AOF.
	reg.Execute(ctx, tokenize("set", "ephemeral", "v", "ex", "3600"))
	// PEXPIRE a key with a long TTL.
	reg.Execute(ctx, tokenize("set", "persistent", "v"))
	reg.Execute(ctx, tokenize("pexpire", "persistent", "86400000")) // 24h

	if err := aof.Close(); err != nil {
		t.Fatal(err)
	}

	// Load into fresh store.
	st2 := store.New(store.WithShards(4))
	reg2 := command.New(st2, command.NewStats())
	n, err := persist.Load(path, reg2)
	if err != nil {
		t.Fatal(err)
	}
	if n < 3 {
		t.Fatalf("expected >= 3 commands, got %d", n)
	}

	// Both keys should exist (TTLs are far in the future).
	for _, key := range []string{"ephemeral", "persistent"} {
		_, ok := st2.Get([]byte(key))
		if !ok {
			t.Errorf("key %q should exist after load", key)
		}
	}
	// Both should have a TTL (positive).
	for _, key := range []string{"ephemeral", "persistent"} {
		ttl, ok := st2.TTL([]byte(key))
		if !ok {
			t.Errorf("key %q should exist for TTL check", key)
			continue
		}
		if ttl <= 0 {
			t.Errorf("key %q should have positive TTL after load, got %v", key, ttl)
		}
	}
}

func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.aof")

	// Create an empty file.
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}

	st := store.New()
	reg := command.New(st, command.NewStats())
	n, err := persist.Load(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 commands, got %d", n)
	}
}

// tokenize builds a [][]byte token slice from strings.
func tokenize(ss ...string) [][]byte {
	tokens := make([][]byte, len(ss))
	for i, s := range ss {
		tokens[i] = []byte(s)
	}
	return tokens
}
