# PLAN.md — mem-x Bootstrap & Build Plan

Owner: orchestrator · Status: bootstrap in progress · Principle: **it has to
work, then be efficient** — correctness is never traded for speed.

---

## 0. Why this plan exists

The user's ask, in order: (1) universal agents file — done (`AGENTS.md`);
(2) plan before coding — this file; (3) a working core: TCP server, RESP
protocol, command parser, dispatcher, in-memory store; (4) growth layers:
classifier routing, security, research, cross-platform builds. The core is
**stdlib-only Go** so `CGO_ENABLED=0` yields static binaries for
linux/darwin/windows and beyond.

Current repo state (the seed to grow from):
- `go.mod` — module `mem-x`, `go 1.27.0` (toolchain already 2026-era). Keep.
- `main.go` — prints a banner. Will become the entry point that wires
  config → store → server → signals.
- `server.go` — **broken stub** (imports `net/http`, calls `net.Listen`;
  does not compile). Replaced in Phase 1 by a real `internal/server` package.
- No tests, no README, not a git repo yet.

---

## 1. Package layout (target)

```
mem-x/
  go.mod                     # go 1.27.0, stdlib-only
  AGENTS.md                  # universal agent registry (done)
  PLAN.md                    # this file
  README.md                  # short, truthful
  cmd/mem-x/main.go          # entry: config → store → server → signal shutdown
  internal/resp/             # RESP codec: read/write, limits, tests, fuzz
  internal/parser/           # command tokenizer (inline + RESP), caps
  internal/command/          # command table, signatures, validation, errors
  internal/store/            # sharded key/value store, TTL heap, atomicity
  internal/server/           # TCP accept loop, per-conn goroutine, dispatcher glue
  internal/config/           # flags/env, defaults, no globals
  internal/version/          # version string for INFO/COMMAND
```

Dependency direction: `server → command → parser+store`, `server → resp`.
No cycles. `internal/` so the public surface stays small until we grow.

---

## 2. Phases

### Phase 1 — Core correctness (the "make it work" phase)
Deliverables, each with tests, `-race` clean, `go vet` clean:
1. **RESP codec** (`internal/resp`): encode simple string `+OK\r\n`, error
   `-ERR ...\r\n`, integer `:N\r\n`, bulk string `$len\r\n...\r\n`, array
   `*N\r\n`; decode arrays of bulk strings (client→server is `*N\r\n$len\r\n`
   form) plus inline commands. Documented caps: max bulk length, max array
   size, max inline line length.
2. **Parser** (`internal/parser`): bytes → command (name + args), validates
   caps, distinguishes RESP vs inline, no allocation per token beyond need.
3. **Dispatcher** (`internal/command`): registry `name → handler`; uniform
   handler signature `(ctx, store, args) → resp reply`; Redis-compatible error
   taxonomy (`ERR`, `WRONGTYPE`, `unknown command`, arity errors).
4. **Store** (`internal/store`): string values; `SET/GET/DEL/EXISTS/EXPIRE/
   TTL/INCR/DECR/APPEND/TYPE/FLUSHDB/PING/ECHO/COMMAND/INFO`. Sharded
   `sync.RWMutex` map (shard count = power of two near `runtime.GOMAXPROCS`),
   expiry via `container/heap` min-heap (lazy check on read + active ticker).
   Every mutation is atomic per key; no lock across I/O.
5. **TCP server** (`internal/server`): `net.Listen`, per-conn goroutine,
   `bufio.Reader/Writer`, read/write deadlines, max concurrent conns,
   per-conn command cap, graceful shutdown via `context` + `os/signal`.
   `recover()` only at the conn boundary; one goroutine per conn, each with a
   defined exit.
6. **Integration tests**: real `net.Conn` over `127.0.0.1` ephemeral port;
   PING/SET/GET round trips; inline + RESP; oversized input rejected; kill
   mid-command; many concurrent clients (`-race`).

**Done =** `redis-cli`-style smoke session works, all tests + `-race` + `-vet`
pass, binary runs with `CGO_ENABLED=0` on linux.

### Phase 2 — Efficiency (the "then be fast" phase, benchmark-gated)
Only what Phase 1 benchmarks prove worth it. Candidates (each must come with a
`bench` verdict before/after):
- `sync.Pool` for read/write buffers and reply scratch.
- Zero-alloc command name lookup (intern command names, compare first byte).
- Fast path: no `string()` conversion of bulk bytes unless the command needs
  it; keys hashed from `[]byte` via `map[string]` (Go's runtime already hashes
  bytes efficiently — verify, don't assume).
- Expiry: switch from heap to a bucketed timing wheel **only if** the heap
  shows up in pprof at scale.
- Batched/`Writev`-style reply flushing per conn.
- GC pressure reduction: value pooling for small values; avoid boxing.

**Done =** `-benchmem` shows measurable, meaningful gains with no correctness
regressions; reviewer + bench gates pass.

### Phase 3 — Agent tooling (the meta layer) ✅

- `classifier` became real: `internal/agent` (deterministic rules engine) +
  `cmd/memx-classify` (CLI). Grades a task line → complexity/type/agent/
  model-tier using the §3 tiers. Routing is reproducible and testable.
- `docs/agent-protocol.md` defines the JSON-lines protocol for spawn
  requests, reports, and gate verdicts.
- `make classify` / `make build-all` builds the tool.

### Phase 4 — Security hardening
- Input caps everywhere (bulk len, inline len, arg count, conn count, command
  rate at the conn level).
- Go native fuzzing: `resp` codec and `parser` fuzz targets run continuously.
- `govulncheck ./...` in the security gate; dependency allowlist enforced.
- Threat model doc (`docs/THREATS.md`): what an attacker can do over the wire,
  what we cap, what we reject.
- `go test -race` + `go vet` in CI; `recover` audit (no panic path reaches the
  process).

### Phase 5 — Cross-platform & release
- Build matrix `GOOS=linux/darwin/windows GOARCH=amd64/arm64 CGO_ENABLED=0`,
  plus `go test` on each where runnable (linux here; darwin/windows in CI).
- Static binaries, `-trimpath`, reproducible builds; release workflow with
  checksums and provenance (when we get to releases).
- No `syscall`-specific logic in core; `os/signal` handles SIGINT/SIGTERM
  cross-platform.

---

## 3. DSA & pattern decisions (with rationale)

| Where | Choice | Why (correctness first, cost-aware) |
|-------|--------|-------------------------------------|
| Key/value storage | Sharded `map[string]entry` with `sync.RWMutex` per shard | O(1) avg, trivial to reason about; sharding cuts contention; Redis itself is effectively a global hash table |
| Expiry | `container/heap` min-heap of (deadline, key) + lazy read check + active ticker | O(log n) expire; correct under concurrency with the shard lock; upgrade to timing wheel only if pprof demands |
| Values | `map[string]string` + typed metadata struct per key | Strings are the v1 data type; no boxing, no interface{} in hot paths |
| Command dispatch | Static table `map[string]handler` (or switch) | O(1) lookup, zero reflection |
| Conn I/O | `bufio` + `sync.Pool` buffers | Bound memory, reuse instead of allocate-per-request |
| Concurrency | One goroutine per conn; `context` cancellation; atomics for counters | Simplest correct model; Go's netpoller makes epoll/kqueue handling free — no manual event loop needed |
| Shutdown | `context.WithCancel` + `sync.WaitGroup` | No goroutine leaks; graceful drain of in-flight commands |

Deliberately **not** doing in v1 (research agent may revisit): RESP3,
pub/sub, sorted sets, skip lists, Lua scripting, replication, AOF/RDB,
memory-arena values, manual epoll. Each is a separate, planned phase with its
own correctness gate.

---

## 4. Testing & quality strategy

- Table-driven unit tests per package; integration tests over real TCP;
  `-race` on every run; `go vet` clean.
- Fuzz targets for `resp` and `parser` (Phase 4 gates them).
- Benchmarks (`-benchmem`) as the arbiter of every optimization.
- Gates per `AGENTS.md` §6: reviewer → security → bench → portability.

---

## 5. Risk register (known, tracked)

| Risk | Mitigation |
|------|------------|
| Broken `server.go` seed | Replaced in Phase 1; nothing depends on it |
| Unbounded network input → OOM | Caps in codec/parser from day one, enforced in Phase 1, fuzzed in Phase 4 |
| Goroutine leak on slow/disconnecting clients | Deadlines + conn cap + WaitGroup shutdown; leak-audited by reviewer |
| Lock contention at scale | Sharding from v1; pprof before any fancy fix |
| Scope creep / gold-plating | Lightweight-first rule; every feature needs a gate + plan entry |
| Cargo-cult "fast patterns" | bench gate requires evidence; research agent cites sources |

---

## 6. Immediate next step (awaiting go-ahead)

Phase 1 as scoped in §2, surgical against the seed: fix/replace `server.go`
into `internal/server`, move `main.go` to `cmd/mem-x`, add `internal/resp`,
`internal/parser`, `internal/command`, `internal/store`, tests, git init,
README. No third-party deps. Nothing else is touched.
