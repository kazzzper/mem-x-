# mem-x Agent Guidelines — how to use the agents

This document explains how the mem-x agent system works day to day: how tasks
flow through the agents, how to read the agent files, and how to run the gates
yourself. It is the companion to `AGENTS.md` (the registry + shared standards)
and the `agents/*.md` files (the per-agent definitions).

---

## 1. Reading the agent files

| File | What it contains |
|------|------------------|
| `AGENTS.md` | Project overview, agent registry summary table, classifier tiers, coding standards, dep policy, workflow, spawning rules, machine-readable YAML registry. |
| `agents/<name>.md` | Full definition for one agent: mission, spawn triggers, output contract, hard rules, routing, and the agents it requests through the orchestrator. |
| `guidelines.md` | This file. How to use the system. |

**Before acting as any agent** (or spawning it), read its `agents/<name>.md`
file. The agent's output contract tells you exactly what to produce; its hard
rules tell you what not to do.

---

## 2. The task flow

Every task follows this pipeline (gates are mandatory checkpoints):

```
task → orchestrator → classifier (tier+type) → planner (if design)
     → engineer (implement) → testwriter (tests) → reviewer (gate 1)
     → security (gate 2) → bench (gate 3, if perf-relevant)
     → portability (gate 4, at milestones)
     → orchestrator accepts → docs/changelog → commit
```

### Step‑by‑step

1. **Orchestrator** receives the task, calls the **classifier**.
2. **Classifier** grades the task (`complexity=S|M|L|XL`, `type=design|code|...`)
   and routes to the right agent + model tier.
3. **Planner** (if design work) produces a plan document with acceptance
   criteria. No code.
4. **Engineer** implements per the plan: code + tests + a note.
5. **Testwriter** extends the test suite to cover every behavior.
6. **Reviewer** (gate 1) reads every changed file and produces findings
   `[blocker|should-fix|nit]`. Zero blockers + zero should-fixes required.
7. **Security** (gate 2) runs `govulncheck`, checks input caps, runs fuzz,
   produces a pass/fail verdict.
8. **Bench** (gate 3, performance-relevant changes only) measures before/after
   with `go test -bench -benchmem`.
9. **Portability** (gate 4, milestones only) builds the matrix
   (linux/darwin/windows, amd64/arm64, CGO_ENABLED=0).
10. **Orchestrator** accepts, **docs** updates changelogs/README, **commit**.

Any gate failure returns to the owning agent with the exact finding.

---

## 3. How the classifier routes

The classifier produces one line per task:

```
task=<id> complexity=<S|M|L|XL> type=<design|code|review|security|research|bench|docs|portability> agent=<id> model=<tier> reason=<short>
```

**Complexity factors:** concurrency, protocol, and memory-safety concerns push
a task up a tier. A mechanical rename is S; a new protocol encoding is XL.

**Model tiers** (AGENTS.md §3):

| Tier | Use for |
|------|---------|
| 1 | docs, formatting, trivial mechanical edits |
| 2 | routine coding, portability, most research |
| 3 | tricky Go, concurrency, reviewer, bench analysis |
| 4 | architecture, protocol design, security, deep concurrency |

Security and protocol tasks never route below tier 3.

---

## 4. Spawning model

The **orchestrator is the only agent that spawns.** Every other agent
**requests** a spawn through the orchestrator when its own mission needs a
peer. See the table in AGENTS.md §7 for the full request graph.

Example: when the `engineer` finishes implementation, the orchestrator spawns
`testwriter`; when `testwriter` finishes, the orchestrator spawns `reviewer`;
and so on.

No agent spawns itself. Requests go to the orchestrator with a reason; the
orchestrator may deny (e.g. "not now — lightweight-first").

---

## 5. Running the gates manually

The gates are automated in the Makefile and scripts. You can run them directly
without going through the agent pipeline:

| Gate | Command | What it does |
|------|---------|-------------|
| Reviewer | `make check` | gofmt → go vet → go test -race → dep gate |
| Reviewer ++ | `make harness` | Above + benchmarks + 5s fuzz |
| Security | `govulncheck ./...` | Scans all deps for CVEs |
| Portability | `make build` (CGO_ENABLED=0) | Builds static binary; manually test with GOOS=windows/darwin |
| Bench | `make bench` | `go test -bench -benchmem` on all packages |

`make harness` is the closest single command to the full gate pipeline.

---

## 6. Working with an AI coding agent

When an AI coding agent (like this one) works on mem-x, it reads the agent
files to determine its role. The typical workflow:

1. **Orchestrator** (the AI's meta‑role) reads `AGENTS.md` + this guidelines
   file.
2. For each task, it calls the **classifier** (or classifies manually).
3. It loads the appropriate `agents/<name>.md` and follows that agent's
   mission, output contract, and hard rules.
4. When the agent's work is done, it requests the next agent via the
   orchestrator (the orchestrator may run the next agent itself or spawn a
   sub‑agent).
5. The gates run in order; failures are routed back to the owning agent.

---

## 7. Prompt engineering practices

Each agent file ends with a **prompt template** — a ready-to-paste system
prompt that instantiates the agent. The templates follow the practices from
the LLM-agents literature (see `docs/research/prompt-engineering-guide.txt`):

- **Role + persona** — every template opens with who the agent is and its
  disposition (e.g. "paranoid in a good way" for security). A defined role
  shapes the output consistently.
- **Context to load** — each agent is told *which files to read* before
  acting (`AGENTS.md`, `PLAN.md`, `docs/research/*.md`). Grounding beats
  guessing.
- **Task + constraints** — the template states the deliverable and the hard
  rules inline, so a spawned agent cannot claim it wasn't told.
- **Output contract** — every template ends with the exact required format
  (routing line, findings list, verdict), which is what makes results
  machine-parseable by the orchestrator.
- **`{task}` / `{code}` / `{plan}` placeholders** — the orchestrator fills
  these with the concrete work item; never embed the task inside the
  template.

When you need a new agent: define it in `agents/<name>.md` using the same
sections (identity → mission → triggers → context → operating instructions →
hard rules → output contract → prompt template), then register it in the
AGENTS.md §1 table, §2 index, and §8 YAML.

## 8. Harness engineering practices

The test harness (`scripts/test-harness.sh`, run via `make harness`) is the
machine-readable gate the agents must pass. Good harness engineering is what
keeps it fast, deterministic, and trustworthy:

- **Deterministic tests, no sleeps.** Expiry tests use the store's fake
  clock (`store.WithClock`) and `atomic`-backed advancement, never
  `time.Sleep`. Flaky tests are reverted, not retried.
- **Race detector always on.** `go test -race ./...` runs in step 4 of the
  harness; every new concurrency test must pass under it.
- **Integration is hermetic.** Server tests start the server on a random
  port, connect, exercise, close — no external Redis, no fixed ports that
  collide. The `go-redis` client (allowlisted, AGENTS.md §5) verifies wire
  compatibility with a real, mature client.
- **Coverage is a floor, not a target.** Steps in the harness report
  coverage; the reviewer flags untested error paths and cap boundaries even
  when the number looks good.
- **Dependency gate is part of the harness.** `scripts/check-stdlib.sh` fails
  the build if the runtime imports anything non-stdlib or a direct dep is off
  the allowlist — so "it builds" and "it's policy-compliant" are checked
  together.
- **Fuzzing is bounded.** The RESP fuzzer runs for a fixed duration (5–10s)
  in CI; a fuzz crash is a blocker, and the reproducer ships with the fix.
- **Benchmarks are meaningful.** `go test -bench -benchmem` with realistic
  payloads, before/after on the same machine; a claimed optimization without
  numbers is treated as not-claimed.

Reference material fetched from the web (RESP spec, Redis command semantics,
prompt-engineering guide) is cached under `docs/research/` with provenance —
agents ground their claims there instead of assuming Redis behavior.

## 9. Current state and future agents

**Phase 1 complete** (core server: TCP, RESP, parser, dispatcher, store).
Phase 2 (efficiency) and Phase 3 (classifier tooling) are next.

The `agents/future.md` file lists agents that are defined but not yet active:
`fuzzer`, `release`, `perf`. They join the registry when the work that
justifies them arrives — not before.

## 10. Note on the web search tool

The `web_search` tool is provided by the DeepSeek harness's
`web-search-deepseek` provider and requires a valid `DEEPSEEK_API_KEY` (stored
via the harness credentials file at `~/.dsh/.credentials.yaml`, the launch
environment, or a literal `apiKey` in the `web-search-deepseek` settings). As
of this writing the configured key is a placeholder, so the tool fails auth.
**Workaround:** agents fetch reference material with direct `curl` and cache
it under `docs/research/` — see the `research` agent's operating instructions.
To restore the tool, set a real key in the credentials file and restart the
harness.