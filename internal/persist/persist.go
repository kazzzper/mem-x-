// Package persist implements AOF (Append-Only File) persistence for mem-x.
// Every write command is appended to a file as a RESP command (the same format
// the parser reads), so the file is a faithful, replayable record of all
// mutations. On startup the file is loaded and replayed, rebuilding the store
// to the exact state before the crash.
//
// Time-relative commands (EXPIRE, PEXPIRE, SET EX/PX) are rewritten to
// absolute-time PEXPIREAT by the time they reach the AOF, so replay is
// wall-clock independent — a server that was down for hours still expires
// keys at the correct absolute deadlines.
package persist

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"mem-x/internal/command"
	"mem-x/internal/resp"
)

// FsyncPolicy controls how often the AOF calls fsync(2).
type FsyncPolicy int

const (
	FsyncAlways   FsyncPolicy = iota // fsync every append (most durable, slowest)
	FsyncEverysec                    // fsync via background ticker (Redis default)
	FsyncNo                          // let the OS decide (fastest, least durable)
)

// ParseFsyncPolicy converts a string to a policy. Accepts "always",
// "everysec", "no" (case-insensitive). Returns FsyncEverysec as default.
func ParseFsyncPolicy(s string) FsyncPolicy {
	switch strings.ToLower(s) {
	case "always":
		return FsyncAlways
	case "no":
		return FsyncNo
	default:
		return FsyncEverysec
	}
}

// AOF manages an append-only command log for durability.
type AOF struct {
	mu     sync.Mutex
	f      *os.File
	w      *resp.Writer
	policy FsyncPolicy
	closed atomic.Bool
}

// Open creates or opens the AOF file at path for append. The file is created
// if it does not exist. policy controls fsync behavior.
func Open(path string, policy FsyncPolicy) (*AOF, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &AOF{
		f:      f,
		w:      resp.NewWriter(f),
		policy: policy,
	}, nil
}

// Append writes one RESP command to the AOF. The call is safe for concurrent
// use (internal mutex). When policy is FsyncAlways, a synchronous fsync(2)
// follows the write.
func (a *AOF) Append(args [][]byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return nil // silently ignore writes after close
	}
	if err := a.w.ArrayLen(len(args)); err != nil {
		return err
	}
	for _, arg := range args {
		if err := a.w.Bulk(arg); err != nil {
			return err
		}
	}
	if err := a.w.Flush(); err != nil {
		return err
	}
	if a.policy == FsyncAlways {
		return a.f.Sync()
	}
	return nil
}

// Sync flushes buffered data and calls fsync(2). Called by the everysec
// background ticker.
func (a *AOF) Sync() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return nil
	}
	if err := a.w.Flush(); err != nil {
		return err
	}
	return a.f.Sync()
}

// Close flushes, syncs, and closes the AOF file.
func (a *AOF) Close() error {
	a.closed.Store(true)
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.w.Flush()
	_ = a.f.Sync()
	return a.f.Close()
}

// Load reads a RESP-command file at path and executes every command through
// reg. Load is idempotent: if the file is missing it returns nil without
// error. The caller must ensure the registry's propagator is nil (or
// disabled) during load to avoid re-appending the entire log.
func Load(path string, reg *command.Registry) (int64, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	lim := resp.DefaultLimits()
	ctx := context.Background()
	var n int64
	for {
		args, err := resp.ReadCommand(br, lim)
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		reg.Execute(ctx, args)
		n++
	}
}
