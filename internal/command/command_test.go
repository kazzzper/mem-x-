package command_test

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"mem-x/internal/command"
	"mem-x/internal/resp"
	"mem-x/internal/store"
)

// exec runs a command through the dispatcher and returns the wire bytes.
func exec(t *testing.T, reg *command.Registry, args ...string) string {
	t.Helper()
	tokens := make([][]byte, len(args))
	for i, s := range args {
		tokens[i] = []byte(s)
	}
	reply := reg.Execute(context.Background(), tokens)
	var buf bytes.Buffer
	w := resp.NewWriter(&buf)
	if err := reply.WriteTo(w); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func newReg(t *testing.T) (*command.Registry, *store.Store) {
	t.Helper()
	st := store.New(store.WithShards(4))
	return command.New(st, command.NewStats()), st
}

func TestPing(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "PING"); got != "+PONG\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "ping", "hello"); got != "$5\r\nhello\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEcho(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "ECHO", "hi"); got != "$2\r\nhi\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetGet(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "SET", "k", "v"); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "k"); got != "$1\r\nv\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "get", "missing"); got != "$-1\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetNX(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "SET", "k", "v", "NX"); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SET", "k", "x", "NX"); got != "$-1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "k"); got != "$1\r\nv\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetXX(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "SET", "k", "v", "XX"); got != "$-1\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "k", "v")
	if got := exec(t, reg, "SET", "k", "x", "XX"); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "k"); got != "$1\r\nx\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSetExpiryOptions(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "SET", "k", "v", "EX", "10"); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "TTL", "k"); !strings.HasPrefix(got, ":") {
		t.Fatalf("got %q", got)
	}
	// bad expire values
	if got := exec(t, reg, "SET", "k2", "v", "EX", "0"); !strings.Contains(got, "invalid expire time") {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SET", "k2", "v", "EX", "abc"); !strings.Contains(got, "value is not an integer") {
		t.Fatalf("got %q", got)
	}
	// conflicting options
	if got := exec(t, reg, "SET", "k2", "v", "NX", "XX"); !strings.Contains(got, "syntax error") {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SET", "k2", "v", "EX", "10", "PX", "100"); !strings.Contains(got, "syntax error") {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SET", "k2", "v", "BOGUS"); !strings.Contains(got, "syntax error") {
		t.Fatalf("got %q", got)
	}
}

func TestArity(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "GET"); got != "-ERR wrong number of arguments for 'get' command\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SET", "k"); !strings.Contains(got, "wrong number of arguments for 'set' command") {
		t.Fatalf("got %q", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "BOGUS"); got != "-ERR unknown command 'bogus'\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "bogus", "a", "b"); !strings.Contains(got, "unknown command 'bogus', with args beginning with: 'a', 'b'") {
		t.Fatalf("got %q", got)
	}
}

func TestIncrDecr(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "INCR", "n"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "INCR", "n"); got != ":2\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "DECR", "n"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "str", "abc")
	if got := exec(t, reg, "INCR", "str"); got != "-ERR value is not an integer or out of range\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestAppend(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "APPEND", "k", "a"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "APPEND", "k", "bc"); got != ":3\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "k"); got != "$3\r\nabc\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTTL(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "TTL", "missing"); got != ":-2\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "k", "v")
	if got := exec(t, reg, "TTL", "k"); got != ":-1\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "k2", "v", "EX", "5")
	if got := exec(t, reg, "TTL", "k2"); got != ":5\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestExpire(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "EXPIRE", "missing", "10"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "k", "v")
	if got := exec(t, reg, "EXPIRE", "k", "10"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "TTL", "k"); got != ":10\r\n" {
		t.Fatalf("got %q", got)
	}
	// non-positive TTL deletes the key
	if got := exec(t, reg, "EXPIRE", "k", "0"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "TTL", "k"); got != ":-2\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "EXPIRE", "k", "abc"); got != "-ERR value is not an integer or out of range\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestDelExists(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "DEL", "a", "b"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "a", "1")
	exec(t, reg, "SET", "b", "2")
	if got := exec(t, reg, "DEL", "a", "b"); got != ":2\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "a", "1")
	if got := exec(t, reg, "EXISTS", "a", "b"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestType(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "SET", "k", "v")
	if got := exec(t, reg, "TYPE", "k"); got != "+string\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "TYPE", "missing"); got != "+none\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFlushDB(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "SET", "k", "v")
	if got := exec(t, reg, "FLUSHDB"); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "k"); got != "$-1\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSelect(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "SELECT", "0"); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SELECT", "1"); got != "-ERR DB index is out of range\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SELECT", "abc"); !strings.Contains(got, "DB index is out of range") {
		t.Fatalf("got %q", got)
	}
}

func TestInfo(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "PING")
	got := exec(t, reg, "INFO")
	if !strings.Contains(got, "mem-x_version:") || !strings.Contains(got, "total_commands_processed:") {
		t.Fatalf("got %q", got)
	}
}

func TestCommand(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "COMMAND"); got != "*0\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestCaseInsensitive(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "sEt", "k", "v")
	if got := exec(t, reg, "GeT", "k"); got != "$1\r\nv\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestMGet(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "SET", "k1", "v1")
	exec(t, reg, "SET", "k2", "v2")
	if got := exec(t, reg, "MGET", "k1", "k2", "missing"); got != "*3\r\n$2\r\nv1\r\n$2\r\nv2\r\n$-1\r\n" {
		t.Fatalf("got %q", got)
	}
	// Empty MGET is rejected by arity (min 1).
	if got := exec(t, reg, "MGET"); !strings.HasPrefix(got, "-ERR wrong number of arguments") {
		t.Fatalf("got %q", got)
	}
}

func TestMSet(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "MSET", "a", "1", "b", "2"); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "a"); got != "$1\r\n1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "b"); got != "$1\r\n2\r\n" {
		t.Fatalf("got %q", got)
	}
	// Odd argument count → error.
	if got := exec(t, reg, "MSET", "a", "1", "b"); !strings.HasPrefix(got, "-ERR wrong number of arguments") {
		t.Fatalf("got %q", got)
	}
}

func TestMSetNX(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "MSETNX", "a", "1", "b", "2"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "MGET", "a", "b"); got != "*2\r\n$1\r\n1\r\n$1\r\n2\r\n" {
		t.Fatalf("got %q", got)
	}
	// a already exists → whole batch aborts.
	if got := exec(t, reg, "MSETNX", "c", "3", "a", "99"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "EXISTS", "c"); got != ":0\r\n" {
		t.Fatalf("got %q (c must not exist after failed MSETNX)", got)
	}
	if got := exec(t, reg, "GET", "a"); got != "$1\r\n1\r\n" {
		t.Fatalf("got %q (a must be untouched)", got)
	}
	// Odd argument count → error.
	if got := exec(t, reg, "MSETNX", "x"); !strings.HasPrefix(got, "-ERR wrong number of arguments") {
		t.Fatalf("got %q", got)
	}
}

func TestGetSet(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "GETSET", "k", "v1"); got != "$-1\r\n" {
		t.Fatalf("missing key GETSET = %q, want null", got)
	}
	if got := exec(t, reg, "GETSET", "k", "v2"); got != "$2\r\nv1\r\n" {
		t.Fatalf("got %q, want old value v1", got)
	}
	if got := exec(t, reg, "GET", "k"); got != "$2\r\nv2\r\n" {
		t.Fatalf("got %q, want v2", got)
	}
}

func TestSetNXCommand(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "SETNX", "k", "v"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "SETNX", "k", "other"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "GET", "k"); got != "$1\r\nv\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStrLen(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "STRLEN", "missing"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "k", "hello")
	if got := exec(t, reg, "STRLEN", "k"); got != ":5\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestIncrByDecrBy(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "INCRBY", "n", "5"); got != ":5\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "INCRBY", "n", "-2"); got != ":3\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "DECRBY", "n", "3"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "s", "abc")
	if got := exec(t, reg, "INCRBY", "s", "1"); !strings.HasPrefix(got, "-ERR") {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "INCRBY", "n", "notanumber"); !strings.HasPrefix(got, "-ERR value is not an integer") {
		t.Fatalf("got %q", got)
	}
}

func TestExpireAt(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "SET", "k", "v")
	// A timestamp in the past deletes the key immediately (Redis semantics).
	if got := exec(t, reg, "EXPIREAT", "k", "1"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "EXISTS", "k"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "EXPIREAT", "missing", "99999999999"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "EXPIREAT", "k", "notanumber"); !strings.HasPrefix(got, "-ERR value is not an integer") {
		t.Fatalf("got %q", got)
	}
}

func TestPExpirePersist(t *testing.T) {
	reg, _ := newReg(t)
	if got := exec(t, reg, "PEXPIRE", "missing", "100"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
	exec(t, reg, "SET", "k", "v")
	if got := exec(t, reg, "PEXPIRE", "k", "500"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "TTL", "k"); got != ":1\r\n" { // ceil(500ms/1s) = 1
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "PERSIST", "k"); got != ":1\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := exec(t, reg, "TTL", "k"); got != ":-1\r\n" {
		t.Fatalf("got %q, want no-TTL marker", got)
	}
	if got := exec(t, reg, "PERSIST", "k"); got != ":0\r\n" {
		t.Fatalf("got %q, want 0 for key without TTL", got)
	}
	if got := exec(t, reg, "PERSIST", "missing"); got != ":0\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestKeys(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "MSET", "user:1", "a", "user:2", "b", "admin", "c")
	got := map[string]bool{}
	r := decodeReply(t, exec(t, reg, "KEYS", "user:*"))
	if r.Kind != resp.RArray || len(r.Array) != 2 {
		t.Fatalf("KEYS user:* = %v, want 2 keys", r)
	}
	for _, k := range r.Array {
		got[string(k.Str)] = true
	}
	if !got["user:1"] || !got["user:2"] || got["admin"] {
		t.Fatalf("KEYS user:* = %v, want exactly user:1 and user:2", got)
	}
	got = map[string]bool{}
	r = decodeReply(t, exec(t, reg, "KEYS", "*"))
	if r.Kind != resp.RArray || len(r.Array) != 3 {
		t.Fatalf("KEYS * = %v, want 3 keys", r)
	}
	for _, k := range r.Array {
		got[string(k.Str)] = true
	}
	for _, want := range []string{"user:1", "user:2", "admin"} {
		if !got[want] {
			t.Fatalf("KEYS * missing %q (got %v)", want, got)
		}
	}
	r = decodeReply(t, exec(t, reg, "KEYS", "nomatch*"))
	if r.Kind != resp.RArray || len(r.Array) != 0 {
		t.Fatalf("KEYS nomatch* = %v, want empty", r)
	}
}

func TestScan(t *testing.T) {
	reg, _ := newReg(t)
	exec(t, reg, "MSET", "k1", "1", "k2", "2", "k3", "3")
	// Full iteration must collect all three keys.
	seen := map[string]bool{}
	cursor := "0"
	guards := 0
	for {
		wire := exec(t, reg, "SCAN", cursor, "MATCH", "k*")
		r := decodeReply(t, wire)
		if r.Kind != resp.RArray || len(r.Array) != 2 {
			t.Fatalf("SCAN reply = %v, want [cursor keys]", r)
		}
		cursor = string(r.Array[0].Str)
		if r.Array[1].Kind != resp.RArray {
			t.Fatalf("SCAN keys element = %v, want array", r.Array[1])
		}
		for _, k := range r.Array[1].Array {
			seen[string(k.Str)] = true
		}
		if cursor == "0" {
			break
		}
		guards++
		if guards > 10 {
			t.Fatal("scan did not terminate")
		}
	}
	if len(seen) != 3 {
		t.Fatalf("scan collected %d keys, want 3", len(seen))
	}
}

// decodeReply runs a raw wire reply through resp.ReadReply.
func decodeReply(t *testing.T, wire string) resp.Reply {
	t.Helper()
	r, err := resp.ReadReply(bufio.NewReader(bytes.NewBufferString(wire)), resp.DefaultLimits())
	if err != nil {
		t.Fatalf("ReadReply(%q): %v", wire, err)
	}
	return r
}

func TestScanSyntax(t *testing.T) {
	reg, _ := newReg(t)
	for _, args := range [][]string{
		{"SCAN", "abc"},
		{"SCAN", "-1"},
		{"SCAN", "0", "MATCH"},
		{"SCAN", "0", "COUNT", "abc"},
		{"SCAN", "0", "BOGUS"},
	} {
		if got := exec(t, reg, args...); !strings.HasPrefix(got, "-ERR") {
			t.Fatalf("exec(%v) = %q, want error", args, got)
		}
	}
}
