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
make build-all    # build the server, memx-cli, and memx-classify binaries
make run          # build + listen on :6379
./mem-x -addr 127.0.0.1:7000 -max-conn 5000 -log-level warn
```

Server flags: `-addr`, `-max-conn`, `-max-bulk-len`, `-max-value-len`,
`-max-args`, `-max-inline-len`, `-idle-timeout`, `-ttl-tick`, `-shards`,
`-aof`, `-appendfsync`, and `-log-level` (`debug|info|warn|error`, default
`info`). If the requested port is already in use the server transparently
reassigns to the next free port and logs a WARN with the actual address — a
busy default never blocks startup. Full table in
[`CONTRIBUTING.md`](CONTRIBUTING.md) §6.

Smoke test:

```sh
printf 'PING\r\n' | nc 127.0.0.1 6379     # → +PONG
printf 'SET k v\r\n' | nc 127.0.0.1 6379   # → +OK
```

## Docker

```sh
docker compose up -d               # start the server
docker compose exec memx memx-url   # print the connection URL
docker compose exec memx memx-cli PING
```

Configuration via `.env` file (copy `.env.example` → `.env`):

| Variable | Default | Description |
|----------|---------|-------------|
| `MEMX_PORT` | 6379 | Server listen & published port |
| `MEMX_PASSWORD` | — | Requirepass (AUTH); builds into the URL |
| `MEMX_TLS` | 0 | 1 = memxs:// (needs cert/key mounts) |
| `MEMX_AOF` | /data/appendonly.aof | AOF persistence path |
| `MEMX_LOG_LEVEL` | info | debug|info|warn|error |

The connection URL is built from the **same variables** — port, password, and TLS
scheme stay in sync between the server and the URL builder. Run `memx-url` in
the container or locally with the same env to get the exact URL:

```sh
MEMX_PASSWORD=secret MEMX_PORT=7000 ./memx-url
# → memx://:secret@localhost:7000
```

## memx-cli — the redis-cli-style client

`memx-cli` talks RESP to the server, prints every reply, and reports the
**per-request round-trip latency in ms** on its own line.

```sh
make build-all                            # produces ./memx-cli

# one-shot mode: command given as arguments
./memx-cli SET k v                        # → OK
./memx-cli MGET k missing                 # → 1) "v"  2) (nil)
./memx-cli -addr 127.0.0.1:7000 KEYS user*

# interactive mode: a prompt, one command per line
./memx-cli -addr 127.0.0.1:7000
127.0.0.1:7000> SET k v
OK
(0.12 ms)
127.0.0.1:7000> GET k
"v"
(0.09 ms)
```

Every reply is followed by its ms latency. Server error replies are shown
redis-cli-style (`(error) ERR ...`); connection problems go to stderr. Type
`QUIT`/`EXIT` to leave, `HELP` for the brief usage. Quote arguments with
double quotes and backslash-escape embedded quotes (`ECHO "say \"hi\""`).

**Auto-spawn:** if no server is running and the address is local
(`127.0.0.1`, `localhost`, `:6379`, …), the CLI boots an **in-process** mem-x
server on that port, connects to it, and stops it when the CLI exits. The
`note:` line on stderr tells you when this happened. Two caveats of the
session-scoped design:

- Each CLI invocation is an **isolated empty instance** — data written in one
  invocation is gone by the next (the server dies with the CLI).
- Within one interactive session the embedded server holds your data normally.

For a persistent shared server (survives across sessions), start the
standalone `./mem-x` yourself and point the CLI at it.

## Persistence (AOF)

mem-x supports **append-only file (AOF) persistence** — every write command is
appended to a RESP-format file so the dataset can be rebuilt on restart, just
like Redis AOF.

**Key design decisions:**

- **Correctness first:** when AOF is enabled, write commands are serialized
  through a global lock so the AOF log order exactly matches the store mutation
  order. Reads remain shard-parallel (no impact).
- **Wall-clock independent replay:** relative TTL commands (`EXPIRE`,
  `PEXPIRE`, `SET EX`/`PX`) are rewritten to absolute `PEXPIREAT` with the
  deadline computed at execution time, so keys that should have expired during
  a server outage are still expired on restart.
- **Fsync policies:** `always` (fsync per append), `everysec` (background
  ticker, the default), `no` (OS decides).
- **Embedded CLI server:** the auto-spawned server is ephemeral and does *not*
  persist — run the standalone `./mem-x` binary for durability.

Enable AOF on startup:

```sh
./mem-x -aof /var/lib/mem-x/appendonly.aof
./mem-x -aof data.aof -appendfsync everysec
```

On restart the file is replayed:

```sh
./mem-x -aof data.aof          # data is restored automatically
```

## Quality gate

```sh
make check       # gofmt + go vet + go test -race + dependency gate
make harness     # full suite: fmt, vet, race tests with coverage, benchmarks, fuzz
make fuzz        # 10s RESP parser fuzz
make classify    # build the classifier tool and show the agent registry
make release     # static binaries for 6 platforms + SHA256 checksums under dist/
```

The dependency gate (`scripts/check-stdlib.sh`) enforces AGENTS.md §5:
runtime code stays stdlib-only; direct test deps must be on the allowlist
(currently `github.com/redis/go-redis/v9`, `github.com/stretchr/testify`).

CI (`.github/workflows/ci.yml`) runs the quality gate, the full harness,
`govulncheck`, and the release matrix on every push/PR to `main`.

## Concurrency & locking

The store is a sharded concurrent map (per-shard `sync.RWMutex`, power-of-two
mask over a per-store `maphash` seed — hash-flooding resistant), with a
separately locked TTL min-heap, `atomic` counters, and strictly-ascending
multi-shard lock ordering so deadlock is structurally impossible. When the AOF
is enabled, write commands additionally serialize through a global
`sync.Mutex` in the command dispatcher so the AOF log order matches the store
mutation order (Redis itself is single-threaded). Every piece is documented in
[`docs/CONCURRENCY.md`](docs/CONCURRENCY.md) and enforced by
`go test -race ./...`.

## Test

```sh
go test -race ./...     # unit + integration (incl. real go-redis client compat)
```

## Classify a task

```sh
echo 'implement a new SET command with EX/PX options' | ./memx-classify
# → task=1 complexity=M type=code agent=engineer model=2 reason=default-code;...
```

## Commands implemented

Strings & keys:

`PING ECHO SET GET DEL EXISTS INCR DECR INCRBY DECRBY APPEND STRLEN TYPE
SETNX GETSET MGET MSET MSETNX`

Expiry:

`EXPIRE EXPIREAT PEXPIRE PEXPIREAT TTL PERSIST`

Iteration:

`KEYS SCAN`

Admin / protocol:

`SELECT INFO COMMAND CLIENT FLUSHDB`

All with Redis-compatible reply and error semantics (missing key → null bulk,
unknown command with args preview, arity errors, integer-out-of-range and
syntax errors).