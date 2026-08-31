// Package command implements the dispatcher: a per-server registry mapping
// lowercased command names to typed handlers, with arity validation and
// error-to-REPLY conversion. There is no package-level mutable state; a
// Registry is built per server (AGENTS.md §4).
package command

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"mem-x/internal/parser"
	"mem-x/internal/resp"
	"mem-x/internal/store"
	"mem-x/internal/version"
)

// Command describes one command and its handler. Handlers are methods on
// *Registry, so they capture the registry and store implicitly.
type Command struct {
	Name    string
	MinArgs int
	MaxArgs int // < 0 means unbounded
	Handler func(ctx context.Context, args [][]byte) (resp.Reply, error)
}

// Stats holds server-wide counters shared between the server and dispatcher.
type Stats struct {
	TotalCommands    atomic.Int64
	ConnectedClients atomic.Int64
	StartTime        time.Time
}

// NewStats returns a fresh Stats.
func NewStats() *Stats { return &Stats{StartTime: time.Now()} }

// Registry holds the command table for one server instance.
type Registry struct {
	store *store.Store
	stats *Stats
	cmds  map[string]Command
}

// New builds a Registry wired to st and stats. It is called once per server.
func New(st *store.Store, stats *Stats) *Registry {
	r := &Registry{store: st, stats: stats, cmds: make(map[string]Command)}
	r.register(Command{Name: "ping", MinArgs: 0, MaxArgs: 1, Handler: r.hPing})
	r.register(Command{Name: "echo", MinArgs: 1, MaxArgs: 1, Handler: r.hEcho})
	r.register(Command{Name: "set", MinArgs: 2, MaxArgs: -1, Handler: r.hSet})
	r.register(Command{Name: "get", MinArgs: 1, MaxArgs: 1, Handler: r.hGet})
	r.register(Command{Name: "del", MinArgs: 1, MaxArgs: -1, Handler: r.hDel})
	r.register(Command{Name: "exists", MinArgs: 1, MaxArgs: -1, Handler: r.hExists})
	r.register(Command{Name: "incr", MinArgs: 1, MaxArgs: 1, Handler: r.hIncr})
	r.register(Command{Name: "decr", MinArgs: 1, MaxArgs: 1, Handler: r.hDecr})
	r.register(Command{Name: "append", MinArgs: 2, MaxArgs: 2, Handler: r.hAppend})
	r.register(Command{Name: "type", MinArgs: 1, MaxArgs: 1, Handler: r.hType})
	r.register(Command{Name: "expire", MinArgs: 2, MaxArgs: 2, Handler: r.hExpire})
	r.register(Command{Name: "ttl", MinArgs: 1, MaxArgs: 1, Handler: r.hTTL})
	r.register(Command{Name: "flushdb", MinArgs: 0, MaxArgs: 0, Handler: r.hFlushDB})
	r.register(Command{Name: "select", MinArgs: 1, MaxArgs: 1, Handler: r.hSelect})
	r.register(Command{Name: "info", MinArgs: 0, MaxArgs: 0, Handler: r.hInfo})
	r.register(Command{Name: "command", MinArgs: 0, MaxArgs: 0, Handler: r.hCommand})
	r.register(Command{Name: "client", MinArgs: 1, MaxArgs: -1, Handler: r.hClient})
	r.register(Command{Name: "mget", MinArgs: 1, MaxArgs: -1, Handler: r.hMGet})
	r.register(Command{Name: "mset", MinArgs: 2, MaxArgs: -1, Handler: r.hMSet})
	r.register(Command{Name: "msetnx", MinArgs: 2, MaxArgs: -1, Handler: r.hMSetNX})
	r.register(Command{Name: "getset", MinArgs: 2, MaxArgs: 2, Handler: r.hGetSet})
	r.register(Command{Name: "setnx", MinArgs: 2, MaxArgs: 2, Handler: r.hSetNX})
	r.register(Command{Name: "strlen", MinArgs: 1, MaxArgs: 1, Handler: r.hStrLen})
	r.register(Command{Name: "incrby", MinArgs: 2, MaxArgs: 2, Handler: r.hIncrBy})
	r.register(Command{Name: "decrby", MinArgs: 2, MaxArgs: 2, Handler: r.hDecrBy})
	r.register(Command{Name: "expireat", MinArgs: 2, MaxArgs: 2, Handler: r.hExpireAt})
	r.register(Command{Name: "pexpire", MinArgs: 2, MaxArgs: 2, Handler: r.hPExpire})
	r.register(Command{Name: "persist", MinArgs: 1, MaxArgs: 1, Handler: r.hPersist})
	r.register(Command{Name: "keys", MinArgs: 1, MaxArgs: 1, Handler: r.hKeys})
	r.register(Command{Name: "scan", MinArgs: 1, MaxArgs: -1, Handler: r.hScan})
	return r
}

func (r *Registry) register(c Command) { r.cmds[c.Name] = c }

// Execute runs one raw command (name first) and returns its reply. It never
// returns an error; every failure becomes a RESP error reply.
func (r *Registry) Execute(ctx context.Context, tokens [][]byte) resp.Reply {
	r.stats.TotalCommands.Add(1)
	cmd, err := parser.Parse(tokens)
	if err != nil {
		return resp.ErrorReply("ERR empty command")
	}
	c, ok := r.cmds[cmd.Name]
	if !ok {
		return resp.ErrorReply(r.unknownCommand(cmd.Name, cmd.Args))
	}
	if len(cmd.Args) < c.MinArgs || (c.MaxArgs >= 0 && len(cmd.Args) > c.MaxArgs) {
		return resp.ErrorReply(fmt.Sprintf("ERR wrong number of arguments for '%s' command", cmd.Name))
	}
	reply, err := c.Handler(ctx, cmd.Args)
	if err != nil {
		return resp.ErrorReply("ERR " + err.Error())
	}
	return reply
}

// unknownCommand builds the Redis-style error for an unknown command name,
// previewing the first few arguments.
func (r *Registry) unknownCommand(name string, args [][]byte) string {
	msg := "ERR unknown command '" + name + "'"
	if len(args) == 0 {
		return msg
	}
	n := min(len(args), 3)
	parts := make([]string, 0, n)
	for _, a := range args[:n] {
		s := string(a)
		if len(s) > 128 {
			s = s[:128]
		}
		parts = append(parts, "'"+s+"'")
	}
	return msg + ", with args beginning with: " + strings.Join(parts, ", ")
}

// Handlers ----------------------------------------------------------------

func (r *Registry) hPing(ctx context.Context, args [][]byte) (resp.Reply, error) {
	if len(args) == 0 {
		return resp.SimpleReply("PONG"), nil
	}
	return resp.BulkReply(args[0]), nil
}

func (r *Registry) hEcho(ctx context.Context, args [][]byte) (resp.Reply, error) {
	return resp.BulkReply(args[0]), nil
}

func (r *Registry) hSet(ctx context.Context, args [][]byte) (resp.Reply, error) {
	key := args[0]
	val := args[1]
	var (
		ttl    time.Duration
		ttlSet bool
		mode   = store.SetAlways
	)
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		opt := strings.ToUpper(string(rest[i]))
		switch opt {
		case "EX", "PX":
			if ttlSet || i+1 >= len(rest) {
				return resp.Reply{}, errors.New("syntax error")
			}
			n, err := strconv.ParseInt(string(rest[i+1]), 10, 64)
			if err != nil {
				return resp.Reply{}, errors.New("value is not an integer or out of range")
			}
			if n <= 0 {
				return resp.Reply{}, errors.New("invalid expire time in 'set' command")
			}
			if opt == "EX" {
				ttl = time.Duration(n) * time.Second
			} else {
				ttl = time.Duration(n) * time.Millisecond
			}
			ttlSet = true
			i++
		case "NX":
			if mode != store.SetAlways {
				return resp.Reply{}, errors.New("syntax error")
			}
			mode = store.SetNX
		case "XX":
			if mode != store.SetAlways {
				return resp.Reply{}, errors.New("syntax error")
			}
			mode = store.SetXX
		default:
			return resp.Reply{}, errors.New("syntax error")
		}
	}
	if !r.store.Set(key, val, ttl, mode) {
		return resp.NullReply(), nil // NX/XX condition failed
	}
	return resp.SimpleReply("OK"), nil
}

func (r *Registry) hGet(ctx context.Context, args [][]byte) (resp.Reply, error) {
	v, ok := r.store.Get(args[0])
	if !ok {
		return resp.NullReply(), nil
	}
	return resp.BulkReply(v), nil
}

func (r *Registry) hDel(ctx context.Context, args [][]byte) (resp.Reply, error) {
	return resp.IntegerReply(int64(r.store.Del(args...))), nil
}

func (r *Registry) hExists(ctx context.Context, args [][]byte) (resp.Reply, error) {
	return resp.IntegerReply(int64(r.store.Exists(args...))), nil
}

func (r *Registry) hIncr(ctx context.Context, args [][]byte) (resp.Reply, error) {
	n, err := r.store.IncrBy(args[0], 1)
	if err != nil {
		return resp.Reply{}, err
	}
	return resp.IntegerReply(n), nil
}

func (r *Registry) hDecr(ctx context.Context, args [][]byte) (resp.Reply, error) {
	n, err := r.store.IncrBy(args[0], -1)
	if err != nil {
		return resp.Reply{}, err
	}
	return resp.IntegerReply(n), nil
}

func (r *Registry) hAppend(ctx context.Context, args [][]byte) (resp.Reply, error) {
	n, err := r.store.Append(args[0], args[1])
	if err != nil {
		return resp.Reply{}, err
	}
	return resp.IntegerReply(int64(n)), nil
}

func (r *Registry) hType(ctx context.Context, args [][]byte) (resp.Reply, error) {
	return resp.SimpleReply(r.store.Type(args[0])), nil
}

func (r *Registry) hExpire(ctx context.Context, args [][]byte) (resp.Reply, error) {
	secs, err := strconv.ParseInt(string(args[1]), 10, 64)
	if err != nil {
		return resp.Reply{}, errors.New("value is not an integer or out of range")
	}
	if secs <= 0 {
		// Redis: a non-positive TTL deletes the key immediately.
		return resp.IntegerReply(int64(r.store.Del(args[0]))), nil
	}
	if r.store.Expire(args[0], time.Duration(secs)*time.Second) {
		return resp.IntegerReply(1), nil
	}
	return resp.IntegerReply(0), nil
}

func (r *Registry) hTTL(ctx context.Context, args [][]byte) (resp.Reply, error) {
	rem, exists := r.store.TTL(args[0])
	if !exists {
		return resp.IntegerReply(-2), nil
	}
	if rem < 0 {
		return resp.IntegerReply(-1), nil
	}
	secs := int64(math.Ceil(rem.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return resp.IntegerReply(secs), nil
}

func (r *Registry) hFlushDB(ctx context.Context, args [][]byte) (resp.Reply, error) {
	r.store.Flush()
	return resp.SimpleReply("OK"), nil
}

func (r *Registry) hSelect(ctx context.Context, args [][]byte) (resp.Reply, error) {
	n, err := strconv.Atoi(string(args[0]))
	if err != nil || n != 0 {
		return resp.Reply{}, errors.New("DB index is out of range")
	}
	return resp.SimpleReply("OK"), nil
}

func (r *Registry) hInfo(ctx context.Context, args [][]byte) (resp.Reply, error) {
	var b strings.Builder
	b.WriteString("# Server\r\n")
	fmt.Fprintf(&b, "mem-x_version:%s\r\n", version.Version)
	fmt.Fprintf(&b, "redis_version:7.0.0\r\n")
	fmt.Fprintf(&b, "os:%s\r\n", runtime.GOOS)
	fmt.Fprintf(&b, "arch:%s\r\n", runtime.GOARCH)
	b.WriteString("# Clients\r\n")
	fmt.Fprintf(&b, "connected_clients:%d\r\n", r.stats.ConnectedClients.Load())
	b.WriteString("# Stats\r\n")
	fmt.Fprintf(&b, "total_commands_processed:%d\r\n", r.stats.TotalCommands.Load())
	fmt.Fprintf(&b, "total_keys:%d\r\n", r.store.Len())
	fmt.Fprintf(&b, "uptime_in_seconds:%d\r\n", int64(time.Since(r.stats.StartTime).Seconds()))
	return resp.BulkReply([]byte(b.String())), nil
}

func (r *Registry) hCommand(ctx context.Context, args [][]byte) (resp.Reply, error) {
	// Minimal COMMAND reply: an empty array is valid RESP and satisfies basic
	// clients. Expanded in a later phase.
	return resp.ArrayReply(nil), nil
}

// hClient handles the CLIENT subcommands real clients (go-redis, redis-cli)
// issue on connect. Only connection-scoped no-ops are supported in this phase.
func (r *Registry) hClient(ctx context.Context, args [][]byte) (resp.Reply, error) {
	switch strings.ToUpper(string(args[0])) {
	case "SETINFO", "SETNAME":
		return resp.SimpleReply("OK"), nil
	case "GETNAME":
		return resp.NullReply(), nil
	case "ID":
		return resp.IntegerReply(1), nil
	default:
		return resp.Reply{}, fmt.Errorf("unknown subcommand '%s' for 'client' command", string(args[0]))
	}
}

// hMGet returns the values for all keys, using null for missing ones. Redis:
// "Returns the values of all specified keys."
func (r *Registry) hMGet(ctx context.Context, args [][]byte) (resp.Reply, error) {
	out := make([]resp.Reply, len(args))
	for i, k := range args {
		if v, ok := r.store.Get(k); ok {
			out[i] = resp.BulkReply(v)
		} else {
			out[i] = resp.NullReply()
		}
	}
	return resp.ArrayReply(out), nil
}

// hMSet sets multiple key/value pairs atomically? No — MSET is NOT atomic in
// Redis; each pair is set independently. Returns OK. Arguments are
// key1 value1 key2 value2 ... so the count must be even.
func (r *Registry) hMSet(ctx context.Context, args [][]byte) (resp.Reply, error) {
	if len(args)%2 != 0 {
		return resp.Reply{}, errors.New("wrong number of arguments for 'mset' command")
	}
	for i := 0; i < len(args); i += 2 {
		r.store.Set(args[i], args[i+1], 0, store.SetAlways)
	}
	return resp.SimpleReply("OK"), nil
}

// hMSetNX sets all pairs only if none of the keys exist (atomic, MSETNX
// semantics). Returns 1 on success, 0 if any key already existed.
func (r *Registry) hMSetNX(ctx context.Context, args [][]byte) (resp.Reply, error) {
	if len(args)%2 != 0 {
		return resp.Reply{}, errors.New("wrong number of arguments for 'msetnx' command")
	}
	pairs := make([][2][]byte, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		pairs = append(pairs, [2][]byte{args[i], args[i+1]})
	}
	if r.store.SetNXMulti(pairs) {
		return resp.IntegerReply(1), nil
	}
	return resp.IntegerReply(0), nil
}

// hGetSet atomically sets key to value and returns the previous value (null
// when the key did not exist). Redis GETSET semantics.
func (r *Registry) hGetSet(ctx context.Context, args [][]byte) (resp.Reply, error) {
	old, ok := r.store.GetSet(args[0], args[1])
	if !ok {
		return resp.NullReply(), nil
	}
	return resp.BulkReply(old), nil
}

// hSetNX sets key to value only if it does not exist. Returns 1 if set, 0 if
// the key already existed (Redis SETNX semantics; the TTL-less form).
func (r *Registry) hSetNX(ctx context.Context, args [][]byte) (resp.Reply, error) {
	if r.store.Set(args[0], args[1], 0, store.SetNX) {
		return resp.IntegerReply(1), nil
	}
	return resp.IntegerReply(0), nil
}

// hStrLen returns the byte length of the value at key, or 0 when missing.
func (r *Registry) hStrLen(ctx context.Context, args [][]byte) (resp.Reply, error) {
	if v, ok := r.store.Get(args[0]); ok {
		return resp.IntegerReply(int64(len(v))), nil
	}
	return resp.IntegerReply(0), nil
}

// hIncrBy increments the integer at key by delta and returns the new value.
func (r *Registry) hIncrBy(ctx context.Context, args [][]byte) (resp.Reply, error) {
	delta, err := strconv.ParseInt(string(args[1]), 10, 64)
	if err != nil {
		return resp.Reply{}, errors.New("value is not an integer or out of range")
	}
	n, err := r.store.IncrBy(args[0], delta)
	if err != nil {
		return resp.Reply{}, err
	}
	return resp.IntegerReply(n), nil
}

// hDecrBy decrements the integer at key by delta and returns the new value.
func (r *Registry) hDecrBy(ctx context.Context, args [][]byte) (resp.Reply, error) {
	delta, err := strconv.ParseInt(string(args[1]), 10, 64)
	if err != nil {
		return resp.Reply{}, errors.New("value is not an integer or out of range")
	}
	n, err := r.store.IncrBy(args[0], -delta)
	if err != nil {
		return resp.Reply{}, err
	}
	return resp.IntegerReply(n), nil
}

// hExpireAt sets an absolute Unix-seconds expiry on key. Returns 1 if set, 0
// if the key does not exist (Redis EXPIREAT semantics; a past timestamp
// deletes the key).
func (r *Registry) hExpireAt(ctx context.Context, args [][]byte) (resp.Reply, error) {
	ts, err := strconv.ParseInt(string(args[1]), 10, 64)
	if err != nil {
		return resp.Reply{}, errors.New("value is not an integer or out of range")
	}
	if r.store.ExpireAt(args[0], ts) {
		return resp.IntegerReply(1), nil
	}
	return resp.IntegerReply(0), nil
}

// hPExpire sets a millisecond TTL on key. Returns 1 if set, 0 if missing
// (Redis PEXPIRE semantics; non-positive TTL deletes the key).
func (r *Registry) hPExpire(ctx context.Context, args [][]byte) (resp.Reply, error) {
	ms, err := strconv.ParseInt(string(args[1]), 10, 64)
	if err != nil {
		return resp.Reply{}, errors.New("value is not an integer or out of range")
	}
	if r.store.Expire(args[0], time.Duration(ms)*time.Millisecond) {
		return resp.IntegerReply(1), nil
	}
	return resp.IntegerReply(0), nil
}

// hPersist removes the TTL from key. Returns 1 if a timeout was removed, 0 if
// the key has no TTL or does not exist (Redis PERSIST semantics).
func (r *Registry) hPersist(ctx context.Context, args [][]byte) (resp.Reply, error) {
	if r.store.Persist(args[0]) {
		return resp.IntegerReply(1), nil
	}
	return resp.IntegerReply(0), nil
}

// hKeys returns all keys matching the glob pattern. This is a full scan and
// O(N) over the dataset, matching Redis KEYS semantics.
func (r *Registry) hKeys(ctx context.Context, args [][]byte) (resp.Reply, error) {
	keys := r.store.Keys(string(args[0]))
	out := make([]resp.Reply, len(keys))
	for i, k := range keys {
		out[i] = resp.BulkReply([]byte(k))
	}
	return resp.ArrayReply(out), nil
}

// hScan implements cursor-based key iteration (Redis SCAN). Reply is
// [cursor, [keys...]] where cursor 0 means iteration complete. Optional
// MATCH <pattern> and COUNT <n> options are honored; COUNT is a hint.
func (r *Registry) hScan(ctx context.Context, args [][]byte) (resp.Reply, error) {
	cursor, err := strconv.Atoi(string(args[0]))
	if err != nil || cursor < 0 {
		return resp.Reply{}, errors.New("invalid cursor")
	}
	pattern := ""
	count := 10 // Redis default COUNT
	for i := 1; i < len(args); i++ {
		switch strings.ToUpper(string(args[i])) {
		case "MATCH":
			if i+1 >= len(args) {
				return resp.Reply{}, errors.New("syntax error")
			}
			i++
			pattern = string(args[i])
		case "COUNT":
			if i+1 >= len(args) {
				return resp.Reply{}, errors.New("syntax error")
			}
			i++
			n, err := strconv.Atoi(string(args[i]))
			if err != nil || n < 0 {
				return resp.Reply{}, errors.New("value is not an integer or out of range")
			}
			count = n
		default:
			return resp.Reply{}, errors.New("syntax error")
		}
	}
	next, keys := r.store.Scan(cursor, count, pattern)
	keyReplies := make([]resp.Reply, 0, len(keys))
	for _, k := range keys {
		keyReplies = append(keyReplies, resp.BulkReply([]byte(k)))
	}
	// Redis SCAN reply: [cursor, [key1, key2, ...]]
	return resp.ArrayReply([]resp.Reply{
		resp.BulkReply([]byte(strconv.Itoa(next))),
		resp.ArrayReply(keyReplies),
	}), nil
}
