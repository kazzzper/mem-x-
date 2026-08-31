package command_test

import (
	"strconv"
	"strings"
	"testing"
)

// propagator wraps a got slice so tests can assert the effective commands the
// registry records when an AOF-style propagator is attached.
func propagator(got *[][]string) func([][]byte) {
	return func(args [][]byte) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = string(a)
		}
		*got = append(*got, parts)
	}
}

func TestPropagateSetNoTTL(t *testing.T) {
	reg, _ := newReg(t)
	var got [][]string
	reg.SetPropagator(propagator(&got))
	exec(t, reg, "SET", "k", "v")
	if len(got) != 1 {
		t.Fatalf("expected 1 propagated command, got %d: %v", len(got), got)
	}
	if strings.Join(got[0], " ") != "SET k v" {
		t.Fatalf("propagated = %v, want [SET k v]", got[0])
	}
}

func TestPropagateSetWithTTL(t *testing.T) {
	reg, _ := newReg(t)
	var got [][]string
	reg.SetPropagator(propagator(&got))
	exec(t, reg, "SET", "k", "v", "EX", "100")
	if len(got) != 2 {
		t.Fatalf("expected 2 propagated commands (SET + PEXPIREAT), got %d: %v", len(got), got)
	}
	if strings.Join(got[0], " ") != "SET k v" {
		t.Fatalf("propagated[0] = %v, want [SET k v]", got[0])
	}
	if got[1][0] != "PEXPIREAT" || got[1][1] != "k" {
		t.Fatalf("propagated[1] = %v, want PEXPIREAT k <abs-ms>", got[1])
	}
	abs, err := strconv.ParseInt(got[1][2], 10, 64)
	if err != nil {
		t.Fatalf("PEXPIREAT arg not an integer: %q", got[1][2])
	}
	if abs <= 0 {
		t.Fatalf("PEXPIREAT absolute time must be positive, got %d", abs)
	}
}

func TestPropagateExpire(t *testing.T) {
	reg, _ := newReg(t)
	var got [][]string
	reg.SetPropagator(propagator(&got))
	exec(t, reg, "SET", "k", "v")
	exec(t, reg, "EXPIRE", "k", "50")
	if len(got) != 2 {
		t.Fatalf("expected 2 propagated commands, got %d: %v", len(got), got)
	}
	// The relative EXPIRE must be rewritten to absolute PEXPIREAT.
	if got[1][0] != "PEXPIREAT" || got[1][1] != "k" {
		t.Fatalf("propagated[1] = %v, want PEXPIREAT k <abs-ms>", got[1])
	}
}

func TestPropagateExpireNegativeDeletes(t *testing.T) {
	reg, _ := newReg(t)
	var got [][]string
	reg.SetPropagator(propagator(&got))
	exec(t, reg, "SET", "k", "v")
	exec(t, reg, "EXPIRE", "k", "-1")
	if len(got) != 2 {
		t.Fatalf("expected 2 propagated commands, got %d: %v", len(got), got)
	}
	if strings.Join(got[1], " ") != "DEL k" {
		t.Fatalf("propagated[1] = %v, want [DEL k]", got[1])
	}
}

func TestPropagateNoOpWrites(t *testing.T) {
	reg, _ := newReg(t)
	var got [][]string
	reg.SetPropagator(propagator(&got))
	// SETNX on an existing key: no write, must not propagate.
	exec(t, reg, "SET", "k", "v")
	exec(t, reg, "SETNX", "k", "other")
	if len(got) != 1 {
		t.Fatalf("SETNX no-op must not propagate; got %d commands: %v", len(got), got)
	}
	// DEL of a missing key: no write, must not propagate.
	exec(t, reg, "DEL", "missing")
	if len(got) != 1 {
		t.Fatalf("DEL no-op must not propagate; got %d commands: %v", len(got), got)
	}
}

func TestPropagateMSetAndMSetNX(t *testing.T) {
	reg, _ := newReg(t)
	var got [][]string
	reg.SetPropagator(propagator(&got))
	exec(t, reg, "MSET", "a", "1", "b", "2")
	exec(t, reg, "MSETNX", "c", "3", "d", "4")
	if len(got) != 2 {
		t.Fatalf("expected 2 propagated commands, got %d: %v", len(got), got)
	}
	if strings.Join(got[0], " ") != "MSET a 1 b 2" {
		t.Fatalf("propagated[0] = %v, want MSET a 1 b 2", got[0])
	}
	// MSETNX success must be recorded as unconditional MSET.
	if strings.Join(got[1], " ") != "MSET c 3 d 4" {
		t.Fatalf("propagated[1] = %v, want MSET c 3 d 4", got[1])
	}
}

func TestNoPropagatorNoPropagation(t *testing.T) {
	reg, _ := newReg(t)
	// No propagator set: SET must not panic and must not record anything.
	exec(t, reg, "SET", "k", "v")
	exec(t, reg, "INCR", "k")
	exec(t, reg, "FLUSHDB")
}

func TestPropagateFlushDB(t *testing.T) {
	reg, _ := newReg(t)
	var got [][]string
	reg.SetPropagator(propagator(&got))
	exec(t, reg, "SET", "k", "v")
	exec(t, reg, "FLUSHDB")
	if len(got) != 2 {
		t.Fatalf("expected 2 propagated commands, got %d: %v", len(got), got)
	}
	if strings.Join(got[1], " ") != "FLUSHDB" {
		t.Fatalf("propagated[1] = %v, want [FLUSHDB]", got[1])
	}
}
