# THREATS.md — threat model for mem-x

This document records what an attacker can do over the wire, what mem-x
caps/prevents, and what remains. It is the security gate's reference
(AGENTS.md — `security` agent, PLAN.md Phase 4). The network is treated as
hostile: every byte from a client is untrusted.

---

## 1. Attack surface

```
client ──TCP──▶ server.Server (accept loop, conn goroutines)
                 │
                 ├── resp.ReadCommand   (protocol parsing, limits)
                 ├── parser.Parse       (token normalization)
                 ├── command.Registry   (dispatch, arity, handlers)
                 ├── store.Store        (sharded map, TTL heap, sweeper)
                 └── config / flags     (operator-supplied)
```

---

## 2. Threats, controls, and status

| # | Threat | Control | Status |
|---|--------|---------|--------|
| T1 | **Memory exhaustion — bulk strings.** Client sends huge `$len` bulk. | `MaxBulkLen` (default 64 MiB) checked in `resp.ReadCommand` before allocation. | ✅ capped |
| T2 | **Memory exhaustion — inline commands.** Long inline line (no `*N`). | `MaxInlineLen` (default 64 KiB) in `readLine`. | ✅ capped |
| T3 | **Memory exhaustion — argument count.** Huge `*N` array header. | `MaxArgs` (default 1 Mi) in `readMultibulk`. | ✅ capped |
| T4 | **Memory exhaustion — connection flood.** Too many concurrent TCP conns. | `MaxConn` semaphore (default 10 000); excess conns rejected immediately. | ✅ capped |
| T5 | **Memory exhaustion — unbounded value growth.** Repeated `APPEND` grows a value past 64 MiB. | `MaxValueLen` (default 512 MiB) in `store.Append` → `ErrValueTooLarge`. | ✅ capped |
| T6 | **Hash-flooding DoS.** Attacker picks keys that collide in the map → O(n) shard lookups. | `hash/maphash` per-`Store` random seed; shards = power-of-two mask. Collisions are seeded + unpredictable. | ✅ mitigated |
| T7 | **Hash-flooding on TTL heap.** Attacker induces many simultaneous expirations. | `container/heap` min-heap; lazy expiry + active sweeper; heap filtered for stale entries. Worst case a scan is O(k) per tick, not per command. | ✅ bounded |
| T8 | **Malformed protocol → panic/crash.** Bad lengths, negative lengths, garbage bytes. | `resp.ReadCommand` bounds every read; no `make([]byte, negative)`; fuzz target `FuzzReadCommand`; `recover()` at conn boundary so a panic kills one conn, never the server. | ✅ tested (fuzz clean) |
| T9 | **Integer overflow.** `INCR`/`DECR`/`EXPIRE` with extreme values. | 64-bit `strconv.ParseInt`; overflow wraps per Go/Redis semantics; invalid values return `-ERR value is not an integer...`. | ✅ |
| T10 | **Command smuggling / unknown commands.** | Dispatch via exact map lookup after lowercase; unknown → `-ERR unknown command` with arg preview (bounded to 3 × 128 chars). | ✅ |
| T11 | **Protocol confusion (RESP type as command).** Sending `+`, `-`, `:`, `$`, `*` where a command is expected. | First byte decides parse path; command parser only accepts well-formed multibulk/inline. | ✅ |
| T12 | **Slowloris / idle connection DoS.** Client connects and never sends. | `IdleTimeout` (0 = none by default; operator can set). | ⚠️ default off |
| T13 | **Unbounded pipelining backlog.** Client fires commands without reading replies. | Replies are written per-command into the conn's `bufio`; backpressure via TCP flow control; no unbounded server-side queue. | ✅ (bounded by socket buffers) |
| T14 | **Log injection.** Attacker-controlled data in logs. | `log/slog` with string values; control bytes not interpreted as log structure. | ✅ |
| T15 | **Path traversal / file access.** | No filesystem access from the server (no `os/exec`, no path from client). | ✅ by design |

---

## 3. Dependency risk

- Runtime core is **stdlib-only** (`scripts/check-stdlib.sh` fails otherwise),
  so the attack surface of the binary is limited to the Go runtime + stdlib.
- Test deps only from the allowlist (AGENTS.md §5): `go-redis/v9`,
  `testify`. Audited with `govulncheck ./...` — **clean** (last run
  2026-08-31, Go 1.27 toolchain).
- New deps must pass the §5 criteria (widely used, MIT/Apache/BSD, zero CVEs,
  shallow tree, vendored, line-reviewed).

---

## 4. Residual risk (accepted)

1. **No authentication.** mem-x does not implement `AUTH`/`REQUIREPASS`.
   Run it only on trusted networks or behind a firewall (e.g. bind to
   loopback / private VLAN). This is the single largest residual risk and is
   tracked for a later phase.
2. **No encryption.** Plain TCP (no TLS). Use a TLS terminator (stunnel,
   sidecar) or a private network for untrusted links.
3. **No per-command rate limit.** An authenticated attacker can issue
   commands as fast as the network allows. CPU work per command is small;
   a malicious client can saturate one core. Tracked for a later phase.
4. **Idle timeout defaults off.** Operators must set `-idle-timeout` to
   protect against slowloris on exposed deployments.
5. **Single-process memory.** Total memory is bounded only by `MaxConn` ×
   per-conn buffers + sum of `MaxValueLen` × live keys. No global memory cap
   yet; an operator who sets `-max-value-len` high accepts that exposure.

---

## 5. Fuzz & verification

- `FuzzReadCommand` (internal/resp) — run 5–10s in `make harness`; extended
  runs in the security gate. Last run: 144k+ execs, zero crashes.
- `go test -race ./...` — all packages (concurrency gate).
- `govulncheck ./...` — dependency CVE scan (clean).
- Smoke test: adversarial RESP frames verified against a live server
  (oversized bulk, malformed trailer, unknown commands).

---

*Reviewed: security gate, Phase 4. Owner: `security` agent. Re-audit on any
protocol/parser/store change.*