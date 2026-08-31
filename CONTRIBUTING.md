# CONTRIBUTING.md — how to work in this repository

This is the operating manual for mem-x: how the repo is laid out, what every
command does, how a change flows through the agent gates, and how we commit.

---

## 1. Repo layout

```
AGENTS.md            agent registry + shared standards (the governing spec)
PLAN.md              the phase roadmap (1 core → 2 efficiency → 3 tooling → 4 security → 5 release)
guidelines.md        how to use the agents day to day
CONTRIBUTING.md      this file — how to operate the repo
docs/
  agent-protocol.md  JSON-lines protocol for orchestrator/agent messages
  research/          reference material fetched from the web (RESP spec, Redis commands, prompt engineering)
agents/*.md          full definition per agent (mission, rules, prompt template)
cmd/
  mem-x/             the server binary
  memx-classify/     the classifier CLI (task → agent/model routing)
internal/
  resp/              RESP codec (reader/writer + limits), fuzz target
  parser/            command tokenizer
  command/           dispatcher + handlers + arity validation
  store/             sharded map, TTL heap, expiry sweeper
  server/            TCP accept loop, conn handling, graceful shutdown
  agent/             classifier rules engine (used by memx-classify)
  config/            defaults + CLI config
  version/           version string
scripts/             test-harness.sh (full suite), check-stdlib.sh (dep gate)
Makefile             the command surface
```

---

## 2. The command surface

Everything is reachable through `make`. All Go caches are pinned inside the
workspace (`.gocache`, `.gomodcache`, `.gopath`) so builds never touch
read-only default cache paths — export `GOCACHE GOMODCACHE GOPATH` via the
Makefile automatically.

| Command | What it does |
|---------|--------------|
| `make build` | build the static server binary `./mem-x` (CGO_ENABLED=0) |
| `make classify` | build `memx-classify` and print the task-type → agent registry |
| `make build-all` | build both `mem-x` and `memx-classify` |
| `make run` | build + start the server on `:6379` |
| `make test` | `go test -race ./...` |
| `make vet` | `go vet ./...` |
| `make fmt` | fail if `cmd/` `internal/` are not gofmt-clean |
| `make bench` | `go test -bench -benchmem ./...` |
| `make harness` | full gate: fmt → vet → race tests + coverage → benchmarks → fuzz |
| `make check` | quick gate: fmt → vet → race tests → dep gate |
| `make fuzz` | 10s RESP parser fuzz |
| `make clean` | remove binaries + local caches |

### The server (`./mem-x`)

```
Usage: ./mem-x [flags]
  -addr string          TCP listen address (default ":6379")
  -max-conn int         max concurrent client connections (default 10000)
  -max-bulk-len int     max bulk string length in bytes (default 67108864 = 64 MiB)
  -max-value-len int64  max stored value size in bytes (default 536870912 = 512 MiB)
  -max-args int         max elements per command (default 1048576)
  -max-inline-len int   max inline command length in bytes (default 65536 = 64 KiB)
  -idle-timeout duration client idle timeout (0 = none)
  -ttl-tick duration    expiry sweeper interval (default 100ms)
  -shards int           store shard count (0 = auto)
```

Example: `./mem-x -addr 127.0.0.1:7000 -max-conn 5000 -ttl-tick 50ms`

### The classifier (`./memx-classify`)

```
Usage: memx-classify [id] [task...]    # grade one task (task = remaining args)
       echo 'task text' | memx-classify  # grade stdin, one task per line
       memx-classify -registry          # list type → agent mapping
```

Output is one routing line per task:

```
task=1 complexity=M type=code agent=engineer model=2 reason=default-code;risk=1;up=0;down=0;promoted-routine-work
```

---

## 3. How a change flows (the agent gates)

Every change goes through the same pipeline (see `guidelines.md` for the
full workflow):

```
task → classifier → planner (if design) → engineer → testwriter
     → reviewer (gate 1) → security (gate 2) → bench (gate 3, if perf)
     → portability (gate 4, at milestones) → accepted → commit
```

The orchestrator is the **only** agent that spawns; all others request spawns
through it (AGENTS.md §7). A gate failure returns to the owning agent with the
exact finding — no skipping gates.

For a human contributor the same discipline applies: write code + tests,
run `make check` locally, then commit in batches (below).

---

## 4. Commit policy — batch, and commit regularly

**Commit in small, logical batches.** One commit = one coherent change that
stands alone. Never dump a whole session into a single commit.

- **Batch by concern:** `internal/agent` (the rules engine) is its own
  commit; `cmd/memx-classify` (the CLI) is another; docs and plan updates a
  third.
- **Commit regularly:** commit each batch as soon as it compiles and passes
  its own tests — do not wait for the end of the session. Small commits are
  easy to review, bisect, and revert.
- **Check before commit:** the batch must pass `go vet` and the relevant
  `go test -race` packages. Run `make check` at each natural milestone.
- **Message format:** `area: short imperative summary` with a body explaining
  *why* when it isn't obvious. Reference AGENTS.md gates when relevant.

Example batches for one feature:

```text
1. feat(classifier): add rules engine grading task → complexity/type/agent/tier
2. feat(classify-cli): add memx-classify CLI + Makefile targets
3. docs: agent protocol, PLAN.md phase 3, README usage
```

This keeps history navigable and makes every commit independently
understandable.

---

## 5. Branch model — test → stage → main

The repository uses three long-lived branches to separate work-in-progress from
integration and release:

| Branch | Role | Who merges into it |
|--------|------|--------------------|
| `test` | Everyday development. All feature work, bug fixes, and experiments land here first. | Pull requests from feature branches |
| `stage` | Integration / pre-release. Changes from `test` are merged here when they are coherent and pass `make check`. | `git merge test` (after review) |
| `main` | Working release. Every commit on `main` is a stable, runnable, tested version. | `git merge stage` (after integration validation) |

**Workflow rules:**
1. **Never commit directly to `main`.** All changes flow through `test` → `stage` → `main`.  
2. **Commit to `test` frequently** (batched by concern, as described in §4).  
3. **Merge `test` → `stage`** when a batch of changes is coherent and passes `make check`.  
4. **Merge `stage` → `main`** only when the state is a working release (all tests pass in race mode, integration tests green, benchmark evidence clean).  
5. **Hotfixes** go through the same pipeline (no fast-track to `main` unless documented and rare).  
6. **Tag releases** from `main` with `git tag vX.Y.Z`.

To start working on a new feature:

```sh
git checkout test
git pull            # ensure latest from remote (if available)
# make changes, commit
git push origin test
```

---

## 6. Server flags reference

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:6379` | TCP listen address |
| `-max-conn` | 10000 | Max concurrent client connections |
| `-max-bulk-len` | 64 MiB | Max bulk string length (bytes) |
| `-max-value-len` | 512 MiB | Max stored value size (bytes) |
| `-max-args` | 1 Mi | Max elements per command |
| `-max-inline-len` | 64 KiB | Max inline command length (bytes) |
| `-idle-timeout` | 0 | Client idle timeout (0 = none) |
| `-ttl-tick` | 1s | Expiry sweeper interval |
| `-shards` | 0 | Store shard count (0 = auto, GOMAXPROCS rounded to power-of-two) |
| `-aof` | (empty) | Append-only persistence file path (empty = disabled) |
| `-appendfsync` | `everysec` | AOF fsync policy: `always` \| `everysec` \| `no` |
| `-tls-cert` | (empty) | TLS certificate (PEM); with `-tls-key` enables TLS (memxs://) |
| `-tls-key` | (empty) | TLS private key (PEM) |
| `-requirepass` | (empty) | Require `AUTH <password>` on every connection (empty = no auth) |
| `-log-level` | `info` | Log level: `debug` \| `info` \| `warn` \| `error` (suppress less-important messages) |

Every flag has an `MEMX_*` environment variable counterpart used by the Docker
image (flags override env): `MEMX_ADDR`/`MEMX_PORT`, `MEMX_PASSWORD`,
`MEMX_TLS_CERT`, `MEMX_TLS_KEY`, `MEMX_AOF`, `MEMX_APPENDFSYNC`,
`MEMX_LOG_LEVEL`, `MEMX_SHARDS`, `MEMX_MAX_CONN`, etc. `cmd/memx-url` builds a
percent-encoded `memx://` / `memxs://` connection URL from the same variables.

**AOF persistence:** with `-aof <path>`, every write command is appended to a
RESP-format log (relative TTLs rewritten to absolute `PEXPIREAT`), and the log
is replayed on startup to restore the dataset. `-appendfsync always` fsyncs
per append (most durable), `everysec` via a background ticker (default), `no`
lets the OS decide. While AOF is enabled, write commands serialize through a
global lock in the dispatcher so the log order matches mutation order. See
`README.md` → Persistence (AOF).

**Port reassignment:** When the requested port is already in use, the server
automatically retries on the next port(s) (up to 10 attempts). A WARN log
line records the deviation. This is transparent to the client — the server
binds the first available port. To disable, start on a port that is free.

---

## 7. Dependency & policy reminders

- **Runtime core is stdlib-only** — enforced by `scripts/check-stdlib.sh`
  inside `make check`. If `make check` fails the dep gate, your code imported
  something it must not (AGENTS.md §5).
- **Test deps only from the allowlist** — currently
  `github.com/redis/go-redis/v9` and `github.com/stretchr/testify`. New deps
  go through the security gate.
- **No `unsafe`, no `os/exec`, no mutable globals, no `init()` side effects** —
  AGENTS.md §4.
- **Ground Redis semantics in `docs/research/`** before claiming a command
  behaves a certain way — don't assume.