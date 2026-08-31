# mem-x

A from-scratch, in-memory Redis-like key/value server in Go.

**Principles:** correctness first, then efficiency · stdlib-only core ·
cross-platform static binaries (`CGO_ENABLED=0` for linux/darwin/windows).

**Status:** bootstrap (agents file + plan landed). Phase 1 — TCP server, RESP
codec, command parser, dispatcher, store — in progress.

```
task → orchestrator → classifier → planner → engineer
     → reviewer → security → bench → portability → done
```

See `AGENTS.md` for the agent registry and coding standards; `PLAN.md` for the
build plan.

## Build & run

```sh
go build -o mem-x ./cmd/mem-x
./mem-x                       # listens on :6379 by default
# smoke test
printf 'PING\r\n' | nc 127.0.0.1 6379
```

## Test

```sh
go test -race ./...
go vet ./...
```
