package command_test

import (
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
