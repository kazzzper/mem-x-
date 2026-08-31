# mem-x

A from-scratch, in-memory Redis-like key/value server in Go.

**Principles:** correctness first, then efficiency · stdlib-only runtime core
(so `CGO_ENABLED=0` yields static binaries for linux/darwin/windows) · vetted
external repos allowed for the test harness (AGENTS.md §5).

**Status:** Phase 1 done — TCP server, RESP codec, command parser, dispatcher,
sharded store all working, race-clean, and gated (reviewer + security +
portability). Phase 2 (efficiency) is next.

```
task → orchestrator → classifier → planner → engineer → testwriter
     → reviewer → security → bench → portability → done
```

See `AGENTS.md` for the agent registry and coding standards; `PLAN.md` for the
build plan.

## Build & run

```sh
make build        # static binary at ./mem-x (CGO_ENABLED=0)
make run          # build + listen on :6379
./mem-x -addr 127.0.0.1:7000 -max-conn 5000
```

Smoke test:

```sh
printf 'PING\r\n' | nc 127.0.0.1 6379     # → +PONG
printf 'SET k v\r\n' | nc 127.0.0.1 6379   # → +OK
```

## Quality gate

```sh
make check       # gofmt + go vet + go test -race + dependency gate
make harness     # full suite: fmt, vet, race tests with coverage, benchmarks, fuzz
make fuzz        # 10s RESP parser fuzz
```

The dependency gate (`scripts/check-stdlib.sh`) enforces AGENTS.md §5:
runtime code stays stdlib-only; direct test deps must be on the allowlist
(currently `github.com/redis/go-redis/v9`, `github.com/stretchr/testify`).

## Test

```sh
go test -race ./...     # unit + integration (incl. real go-redis client compat)
```

## Commands implemented

`PING ECHO SET GET DEL EXISTS INCR DECR APPEND TYPE EXPIRE TTL FLUSHDB
SELECT INFO COMMAND CLIENT` — with Redis-compatible reply and error semantics
(missing key → null bulk, unknown command with args preview, arity errors).
