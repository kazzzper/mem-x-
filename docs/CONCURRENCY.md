# CONCURRENCY.md — mem-x locking & concurrency model

This document is the authoritative description of how mem-x handles
concurrency: what is shared, what locks protect it, the lock-ordering rules
that prevent deadlock, and the edge cases the model is designed to absorb.
Everything here is enforced by `go test -race ./...` (`make check`) and the
integration + fuzz suites (`make harness`).

---

## 1. The shared state

mem-x has three independently-synchronized pieces of shared state:

| State | Protected by | Granularity |
|-------|--------------|-------------|
| Key/value data | per-shard `sync.RWMutex` | 1 of N shards |
| Expiry heap | `Store.expMu sync.Mutex` | one heap |
| Server connection registry | `sync.Map` | per connection |
| Buffers | `sync.Pool` (`readerPool`/`writerPool`) | per goroutine |
| Counters | `atomic.Int64` | word-sized |

There is **no single global lock** on the data store: contention is bounded
by the shard count.

---

## 2. Sharded concurrent map

The store is `[]*shard`, where each shard is:

```go
type shard struct {
    mu sync.RWMutex
    m  map[string]entry
}
```

- Shard count = `GOMAXPROCS` rounded up to a power of two, clamped to
  `[8, 256]` (override with `-shards`).
- A key maps to shard `maphash.String(seed, key) & (len-1)` — a power-of-two
  mask over a strong 64-bit hash.
- The `maphash.Seed` is **generated once per Store** (`maphash.MakeSeed()`),
  so two processes (and two Stores) use different hashings. This defeats
  hash-flooding: an attacker cannot pick keys that all collide into one shard
  unless they can guess the runtime seed.

### Single-key operations

`Set`, `Get`, `Del`, `GetSet`, `ExpireAt`, `Persist`, `IncrBy` lock **exactly
one shard**:

- readers take `RLock` (concurrent with each other),
- writers take `Lock` (exclusive).

No operation ever holds a shard lock across network I/O — the store is
synchronous and pure in-memory, so lock hold times are microseconds.

---

## 3. Lock ordering (deadlock-freedom)

Two operations touch more than one shard: `MSETNX` (`SetNXMulti`) and
`FLUSHDB`. Both follow the same rule:

> **Lock all involved shards in ascending shard-index order, and unlock in
> reverse.**

- `SetNXMulti` collects the unique shards of every key in the batch, sorts
  the indices ascending, locks each, and unlocks in reverse order.
- `FlushDB` locks every shard, ascending.

Because every multi-shard lock sequence is strictly ascending, two such
operations can never hold overlapping shard locks in opposite order — the
classic deadlock condition is structurally impossible. Single-shard
operations lock one shard at a time and are trivially deadlock-free with
respect to the multi-shard ones (they never need a second shard while
holding one).

### Expiry heap vs. shard locks

The TTL min-heap (`exp`) is guarded by its own `expMu`:

- Writers (`Set ... EX/PX`, `ExpireAt`) hold a **shard lock**, then acquire
  `expMu` via `pushExpiry`. Nesting is strictly **shard → expMu**.
- The sweeper (`sweep()`) acquires `expMu`, pops all due entries, **releases
  `expMu`**, and only then takes each shard lock to purge. It never holds
  `expMu` while taking a shard lock.

So the global lock-order rule is:

> **shard locks → expMu**, never the reverse.

`sweep()` deliberately reorders to shard-after-heap by *dropping* the heap
lock before touching any shard. This keeps a `Set` that pushes into the heap
from ever deadlocking against a sweeper that wants a shard.

---

## 4. Expiry: lazy + active, with stale-entry tolerance

TTL expiry is enforced two ways, and the heap is designed to tolerate stale
entries:

1. **Lazy check on access.** Every read/write of a key calls `expired(&e)`
   (compares `entry.expireAt` against `now`) and purges expired keys on
   contact.
2. **Active sweep.** A ticker (default `-ttl-tick 1s`) calls `sweep()`: it
   pops every heap entry whose `deadline <= now`, then for each popped key
   takes the shard lock and deletes **only if the key's current `expireAt`
   still equals the popped deadline**.

The equality check is what makes the heap safe under concurrent
modification: a key that was expired, then re-set with a new TTL (pushing a
new heap entry), is left alone when the sweeper visits the stale entry —
the `expireAt` no longer matches, so nothing is deleted. Stale heap entries
are harmless: they are popped once and dropped.

`count` (the key counter) is `atomic.Int64`, updated at the same time as map
writes and read by `Len()` lock-free.

---

## 5. Server concurrency

- **Accept loop** accepts connections; each connection runs in its own
  goroutine (one goroutine per client — no async event loop).
- The connection registry is a `sync.Map` keyed by the connection, so
  graceful shutdown can enumerate and close every live connection exactly
  once.
- `sync.WaitGroup` tracks in-flight handlers so shutdown drains commands
  before returning.
- Per-connection `bufio.Reader` and `resp.Writer` come from `sync.Pool` and
  are returned on close — allocation-free steady-state connection handling.
- `Stats.TotalCommands` and `Stats.ConnectedClients` are `atomic.Int64` —
  updated lock-free from every connection goroutine.

---

## 6. Edge cases the model absorbs

| Case | Behavior |
|------|----------|
| `GETSET` on a key with a TTL | Write clears the TTL (Redis GETSET semantics) |
| `MSETNX` with any key already existing | Whole batch aborts; no key is written |
| `MSETNX` with an expired-but-present key | Expired key is purged first; batch proceeds (Redis semantics) |
| `EXPIREAT` with a past timestamp | Key deleted immediately |
| `PERSIST` on a key with no TTL | Returns 0, key untouched |
| `KEYS` / `SCAN` mid-mutation | RLock per shard; a key is never read partially; may be returned twice across a scan (Redis guarantee) |
| `SCAN` cursor semantics | One shard per call; cursor 0 = done; all keys returned exactly once in the steady-state case |
| Concurrent `SET EX` + `PERSIST` | Serialized by the shard lock; final state is one of the two, never a torn mix |
| Heap staleness (re-set after expire) | Sweeper's `expireAt == deadline` check prevents wrong deletes |

---

## 7. What the tests prove

- `make check` runs the whole suite under the race detector (`go test -race
  ./...`).
- `internal/store` unit tests cover the multi-shard lock ordering, the
  expired-aborts-`MSETNX` path, and the `GetSet`/`ExpireAt`/`Persist` TTL
  edge cases.
- `internal/server` integration tests (`TestConcurrentClients`) hammer many
  connections at once.
- The go-redis integration harness drives a *real* client against the running
  server, proving the wire behavior — and its concurrency — is what real
  clients expect.
- `internal/resp` has a fuzz target for the parser; fuzzing runs in
  `make harness`.

Rule of thumb: if a change touches locking, it must still pass
`go test -race ./...` with no output other than `ok`, or it does not land.
