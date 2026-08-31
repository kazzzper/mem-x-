# AGENTS.md — mem-x Universal Agent Registry

This file is the **single source of truth** for the agent system on this
repository: the registry, shared standards, and the workflow. Each agent's
full definition lives in its own file under `agents/` (indexed in §2 below).
The **orchestrator** reads this registry to spawn agents; the **classifier**
grades every task and routes it to the right agent + model tier; **reviewer**
and **security** gate every change before it lands. See `guidelines.md` for
how to use the agents day to day.

> Rule of thumb for all agents: **correctness first, then efficiency.**
> It has to *work*, then be *fast*. Never trade correctness for speed.

---

## 0. The project in one paragraph

**mem-x** is a from-scratch, in-memory Redis-like key/value server written in
Go. Core architecture: TCP listener → RESP codec → command parser → dispatcher →
in-memory store. The **runtime core is stdlib-only** (zero third-party runtime
deps), so it cross-compiles to any platform with `CGO_ENABLED=0` (linux,
darwin, windows, and beyond); the **test harness may use vetted external
repos** (§5). It must be lightweight before it grows: every layer starts minimal,
correct, and tested; optimization is a separate, benchmark-driven phase. Agents
apply systems-engineering fundamentals — DSA, memory discipline, concurrency
hygiene — without gold-plating.

---

## 1. Agent registry (summary)

| ID | Agent | Mission | Model tier |
|----|-------|---------|------------|
| `orchestrator` | Universal spawner | Reads this file, routes work, runs gates | XL |
| `planner` | Architect | Decomposes work into plans before any code | L |
| `classifier` | Task router | Grades complexity + task type → agent + model | S |
| `engineer` | Senior systems engineer | Implements (Go), correctness-first | L |
| `testwriter` | Test writer | Writes/extends the test suite to spec, race-clean | M |
| `reviewer` | Code reviewer | Catches bugs, races, leaks, growing patterns | L |
| `security` | Security engineer | Threat model, fuzzing, dep audit, hardening | L |
| `research` | Pattern researcher | Finds cleverer DSA/patterns, with evidence | M |
| `bench` | Benchmark engineer | Proves or kills optimizations with numbers | M |
| `portability` | Cross-platform tester | Verifies linux/darwin/windows builds + runs | S |
| `docs` | Writer | User + contributor docs, changelogs | S |

Suggested future agents (spawn when the work shows up, not before): `fuzzer`
(continuous adversarial input), `release` (CI/CD, static binaries, signing),
`perf` (profile-guided optimization, pprof deep-dives). See
`agents/future.md`.

---

## 2. Agent files (full definitions)

Each agent's full definition — mission, spawn triggers, output contract, hard
rules, routing, and the spawns it requests through the orchestrator — lives in
its own file. Load the relevant file before acting as or spawning that agent:

| Agent | File |
|-------|------|
| orchestrator | [`agents/orchestrator.md`](agents/orchestrator.md) |
| planner | [`agents/planner.md`](agents/planner.md) |
| classifier | [`agents/classifier.md`](agents/classifier.md) |
| engineer | [`agents/engineer.md`](agents/engineer.md) |
| testwriter | [`agents/testwriter.md`](agents/testwriter.md) |
| reviewer | [`agents/reviewer.md`](agents/reviewer.md) |
| security | [`agents/security.md`](agents/security.md) |
| research | [`agents/research.md`](agents/research.md) |
| bench | [`agents/bench.md`](agents/bench.md) |
| portability | [`agents/portability.md`](agents/portability.md) |
| docs | [`agents/docs.md`](agents/docs.md) |
| (future) | [`agents/future.md`](agents/future.md) |

Editing rule: agent definitions are amended in their own file; `AGENTS.md`
is amended only through the orchestrator with a reason.

---

## 3. Classifier model tiers

| Tier | Quality bar | Use for |
|------|-------------|---------|
| 1 | fast/cheap | docs, formatting, trivial mechanical edits |
| 2 | standard | routine coding, portability checks, most research |
| 3 | strong | tricky Go, concurrency, reviewer, bench analysis |
| 4 | strongest | architecture, protocol design, security, deep concurrency |

Security and protocol tasks **never** route below tier 3.

---

## 4. Coding standards (2026 Go, applied to this repo)

1. **stdlib-first.** The runtime core has zero third-party runtime dependencies.
2. **Formatting & tooling:** `gofmt`, `go vet`, `go test -race` clean at all
   times. Static analysis via `go vet`; `govulncheck` for the module.
3. **Errors:** wrap with `%w`, use `errors.Is/As`, `errors.Join` for
   fan-in. Never swallow errors at the edge — log via `log/slog` with
   context (`slog.Int`/`slog.String`), no `fmt.Println` in server code.
4. **Context:** `context.Context` is the first parameter of any function that
   blocks or does I/O; honor cancellation in listeners, conn loops, expiry
   workers.
5. **No globals:** no mutable package-level state; pass dependencies
   explicitly. No `init()` side effects.
6. **Concurrency:** prefer `sync/atomic` for counters; `sync.Mutex/RWMutex`
   only around actual shared state; shard to reduce contention. Never hold a
   lock across network I/O.
7. **Memory discipline:** reuse buffers (`sync.Pool`) in hot paths; avoid
   allocation-per-command; bound every read from the network.
8. **Safety:** no `unsafe`, no reflection in hot paths, no `os/exec`.
9. **Modern stdlib idioms:** `clear()`, `min/max`, generics where they remove
   duplication without obscuring, `slices`/`maps` packages, `crypto/rand` for
   anything random, `log/slog` for logging.
10. **Cross-platform:** `CGO_ENABLED=0`; `path/filepath`; `os/signal` for
    shutdown; never assume `GOOS`-specific behavior in core.

---

## 5. Dependency policy (secure open-source Go deps)

- **Runtime core: stdlib-only.** The server binary builds with
  `CGO_ENABLED=0` and zero third-party runtime imports — this keeps the
  cross-platform story trivial (PLAN.md §0).
- **Test/tooling deps: allowed from the allowlist.** Test harnesses and
  tooling use trusted, widely-adopted libraries instead of reinventing
  wheels. Any new dep must be: (a) widely used and maintained, (b)
  MIT/Apache-2.0/BSD licensed, (c) zero known CVEs (`govulncheck` clean),
  (d) a shallow dependency tree, (e) vendored (`go mod vendor`) for hermetic
  builds, and (f) reviewed line-by-line at the pinned version.
- **Allowlist (current):**
  - `github.com/redis/go-redis/v9` — official Redis client; the integration
    harness uses it to verify RESP wire compatibility. MIT.
  - `github.com/stretchr/testify` — Go test assertions. MIT.
- **Review cadence:** `govulncheck ./...` runs in every security pass; pin
  exact versions; updates go through the same gate. The maintainer retains
  final veto over every dependency.

---

## 6. Standard workflow (the gates)

```
task → orchestrator → classifier (tier+type) → planner (if design) 
     → engineer (implement) → testwriter (tests) → reviewer (gate 1) → security (gate 2)
     → bench (gate 3, if perf-relevant) → portability (gate 4, at milestones)
     → orchestrator accepts → docs/changelog → commit
```

Every gate failure returns to the owning agent with the exact finding.
Skipping a gate is a blocker. This file is amended only through the
orchestrator with a reason.

---

## 7. Spawning between agents

The **orchestrator is the only agent that actually spawns.** Every other agent
**requests** a spawn through the orchestrator when its own mission needs a
peer. Each agent knows whom it needs and when:

| Requester | Requests | When |
|-----------|----------|------|
| orchestrator | classifier | start of every task |
| classifier | planner | task type is design, or complexity ≥ L |
| classifier | engineer | task type is code |
| planner | research | design question needs evidence |
| planner | engineer | plan is approved |
| engineer | testwriter | implementation is done (before review) |
| engineer | research | "is there a faster way" question mid-implementation |
| testwriter | reviewer | test suite is complete |
| reviewer | engineer | findings need fixing (gate failure) |
| reviewer | security | review is clean |
| security | engineer | hardening findings need fixing |
| security | research | new attack surface needs evidence |
| security | portability | milestone or net/os/signal change |
| reviewer/bench | bench | optimization claim needs numbers |
| bench | engineer | an optimization needs implementing or reverting |
| reviewer | docs | behavior changed at acceptance |

Rules:
- No agent spawns itself. Requests go to the orchestrator with a reason; the
  orchestrator may deny (e.g. "not now — lightweight-first").
- Gates are the only *mandatory* spawn points (reviewer → security → bench →
  portability); all other spawns are on-demand.
- A spawned agent reports back to the requester through the orchestrator; the
  requester does not own the spawned agent's lifecycle.

---

## 8. Machine-readable registry (for the orchestrator)

```yaml
project: mem-x
lang: go
toolchain: go1.27.0
core_deps: stdlib-only (runtime); allowlisted test deps
platforms: [linux, darwin, windows]
agent_files: agents/<id>.md
agents:
  - id: orchestrator
    mission: route work, spawn agents, run gates
    model_tier: 4
    spawns: [classifier, planner, engineer, testwriter, reviewer, security, research, bench, portability, docs]
  - id: planner
    mission: plans before code
    model_tier: 3
    output: plan-doc
  - id: classifier
    mission: grade complexity + type, route to agent + model tier
    model_tier: 1
    output: routing-line
  - id: engineer
    mission: implement Go, correctness-first
    model_tier: 3
    output: code + tests + note
    requests: [testwriter, research]
  - id: testwriter
    mission: write/extend tests to spec, race-clean
    model_tier: 2
    output: tests + green run log
    requests: [reviewer]
  - id: reviewer
    mission: catch bugs, leaks, races, growing patterns
    model_tier: 3
    output: findings [blocker|should-fix|nit]
  - id: security
    mission: threat model, fuzz, dep audit, input caps
    model_tier: 4
    output: verdict pass/fail
  - id: research
    mission: evidence-backed pattern/DSA research
    model_tier: 2
    output: options + citations + recommendation
  - id: bench
    mission: prove/kill optimizations with numbers
    model_tier: 2
    output: before/after bench verdict
  - id: portability
    mission: verify linux/darwin/windows builds + runs
    model_tier: 1
    output: build matrix result
  - id: docs
    mission: truthful docs and changelogs
    model_tier: 1
    output: doc diffs
gates:
  - reviewer
  - security
  - bench
  - portability
rules:
  correctness_before_efficiency: true
  stdlib_first: true
  no_globals: true
  surgical_changes_only: true
  cgo_disabled: true
  no_unsafe: true
  no_os_exec: true
```
