# mem-x

A from-scratch, in-memory Redis-like key/value server in Go.

**Principles:** correctness first, then efficiency · stdlib-only runtime core
(so `CGO_ENABLED=0` yields static binaries for linux/darwin/windows) · vetted
external repos allowed for the test harness (AGENTS.md §5).

**Status:** Phase 1 done — TCP server, RESP codec, command parser, dispatcher,
sharded store all working, race-clean, and gated (reviewer + security +
portability). Phase 2 (efficiency) is next.

## Requirements

- **Go 1.27+** — the module pins `go 1.27.0` in go.mod.
- **CGO_ENABLED=0** — the binary builds fully static; no C toolchain needed.
- **OS/arch** — linux (amd64, arm64), darwin (amd64, arm64), windows (amd64).
  Cross-compile with `GOOS`/`GOARCH`:
  ```sh
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o mem-x.exe ./cmd/mem-x
  ```
- **Runtime deps: none** — the server binary has zero third-party dependencies.
  See AGENTS.md §5 for the dependency policy.
- **Test deps (allowlisted):**
  - `github.com/redis/go-redis/v9 v9.22.0` — integration harness (MIT)
  - `github.com/stretchr/testify v1.12.1` — test assertions (MIT)
  Network access is needed once to download them; after that `GOMODCACHE`
  (pinned to `.gomodcache/` by the Makefile) avoids re‑downloading.
- **Build tooling:** `make`, `gofmt`, `go vet`, `govulncheck` (optional).

## Agent system

The repository uses an agent‑based workflow for development. See:

- [`AGENTS.md`](AGENTS.md) — the agent registry, coding standards, dep policy,
  and workflow.
- [`agents/`](agents/) — one file per agent with full definition (mission,
  spawn triggers, output contract, hard rules).
- [`guidelines.md`](guidelines.md) — how to use the agents day to day.

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
make classify    # build the classifier tool and show the agent registry
make build-all   # build the server + classifier binaries
```

The dependency gate (`scripts/check-stdlib.sh`) enforces AGENTS.md §5:
runtime code stays stdlib-only; direct test deps must be on the allowlist
(currently `github.com/redis/go-redis/v9`, `github.com/stretchr/testify`).

## Test

```sh
go test -race ./...     # unit + integration (incl. real go-redis client compat)
```

## Classify a task

```sh
echo 'implement a new SET command with EX/PX options' | ./memx-classify
# → task=1 complexity=M type=code agent=engineer model=2 reason=default-code;...

## Test

```sh
go test -race ./...     # unit + integration (incl. real go-redis client compat)
```

## Commands implemented

`PING ECHO SET GET DEL EXISTS INCR DECR APPEND TYPE EXPIRE TTL FLUSHDB
SELECT INFO COMMAND CLIENT` — with Redis-compatible reply and error semantics
(missing key → null bulk, unknown command with args preview, arity errors).