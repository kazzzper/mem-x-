# Agent Protocol — JSON lines for the orchestrator

> Defined in `internal/agent` and `cmd/memx-classify`. See `AGENTS.md` §7
> (spawning) and `agents/classifier.md` (classifier output contract).

The orchestrator and agents communicate over **JSON lines** (one JSON object
per line, each object self-contained). This doc describes the three message
types: **routing**, **report**, and **gate verdict**.

---

## 1. Routing (classifier → orchestrator)

The classifier emits one line per task (the `Line()` method on `Route`):

```json
{"event":"route","task":"42","complexity":"M","type":"code","agent":"engineer","model":2,"reason":"default (code); risk=1 up=0 down=0"}
```

Fields:
- `event` — always `"route"`.
- `task` — the task id assigned by the orchestrator.
- `complexity` — `S`, `M`, `L`, or `XL`.
- `type` — `design`, `code`, `review`, `security`, `research`, `bench`,
  `docs`, or `portability`.
- `agent` — the owning agent id (from the registry).
- `model` — the model tier (`1`–`4`).
- `reason` — human-readable explanation of the decision.

The orchestrator parses the `agent` field and routes the task to that agent.

---

## 2. Report (agent → orchestrator)

Every agent reports its output as a JSON line with a `report` event:

```json
{"event":"report","task":"42","agent":"engineer","status":"ok","output":{"summary":"implemented SET with EX/PX/NX/XX","files":["internal/command/command.go"],"changes":95},"findings":null}
```

Fields:
- `event` — always `"report"`.
- `task` — the task id.
- `agent` — the reporting agent id.
- `status` — `"ok"`, `"failed"`, or `"request"` (when the agent requests a
  spawn through the orchestrator).
- `output` — free-form JSON object with the agent's deliverable.
- `findings` — null or an array of `{severity, file, line, message}` objects
  (used by the reviewer gate).

---

## 3. Gate verdict (reviewer / security / bench / portability → orchestrator)

Each gate produces a verdict JSON line:

```json
{"event":"gate","task":"42","gate":"reviewer","status":"pass","findings":[]}
```

```json
{"event":"gate","task":"42","gate":"security","status":"fail","findings":[{"severity":"blocker","file":"internal/resp/resp.go","line":42,"message":"unbounded bulk length in inline path"}]}
```

Fields:
- `event` — always `"gate"`.
- `task` — the task id.
- `gate` — the gate id (`reviewer`, `security`, `bench`, `portability`).
- `status` — `"pass"` or `"fail"`.
- `findings` — array of findings; each finding has `severity`
  (`blocker|should-fix|nit`), `file`, `line`, and `message`. Empty on pass.

A `fail` verdict with any `blocker` or `should-fix` finding returns the task
to the owning agent. The orchestrator records the gate trail and re-runs
gates until all pass.

---

## 4. Spawn request (agent → orchestrator, embedded in a report)

When an agent needs a peer spawned, it sets `status: "request"`:

```json
{"event":"report","task":"42","agent":"engineer","status":"request","request":{"spawn":"testwriter","reason":"implementation done, before review"},"output":null}
```

The orchestrator reads `request.spawn`, validates it against the spawn graph
(AGENTS.md §7), and spawns the requested agent.