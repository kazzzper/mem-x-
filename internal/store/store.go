// Package store implements a sharded, in-memory key/value store with TTL
// expiry. The store is safe for concurrent use.
//
// Keys are []byte (the wire format), hashed with maphash.Bytes. The map
// lookup uses inline string(key) conversion for the compiler's zero-alloc
// elision (m[string(b)] does not allocate). The expiry heap stores string
// keys; conversions on that path are rare (TTL writes only).
package store

import (
	"container/heap"
	"context"
	"errors"
	"hash/maphash"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotInteger is returned by integer-only operations when the stored value
// is not a valid 64-bit integer.
var ErrNotInteger = errors.New("value is not an integer or out of range")

// ErrValueTooLarge is returned when a write would exceed the maximum allowed
// value size. This bounds the unbounded APPEND growth path (AGENTS.md §2.6).
var ErrValueTooLarge = errors.New("string exceeds maximum allowed size")

// Option is a functional option for Store.
type Option func(*Store)

// WithClock overrides the time source (for testing).
func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		s.now = now
	}
}

// WithShards sets the number of shards (rounded up to a power of two).
// Values <= 0 keep the automatic default.
func WithShards(n int) Option {
	return func(s *Store) {
		if n <= 0 {
			return
		}
		p := nextPow2(n)
		s.shards = make([]*shard, p)
		for i := range s.shards {
			s.shards[i] = &shard{m: make(map[string]entry)}
		}
	}
}

// WithMaxValueLen caps the size of any single stored value (bytes).
// Values <= 0 keep the default (512 MiB, matching Redis proto-max-bulk-len).
func WithMaxValueLen(n int64) Option {
	return func(s *Store) {
		if n > 0 {
			s.maxValLen = n
		}
	}
}

// Store is a sharded, in-memory key/value store.
type Store struct {
	shards    []*shard
	seed      maphash.Seed
	now       func() time.Time
	count     atomic.Int64
	maxValLen int64

	expMu sync.Mutex
	exp   expHeap
}

// New returns a Store with shards sized from GOMAXPROCS (rounded to a power
// of two, clamped to [8, 256]) unless overridden by WithShards.
func New(opts ...Option) *Store {
	s := &Store{
		shards:    make([]*shard, nextPow2(clamp(runtime.GOMAXPROCS(0), 8, 256))),
		seed:      maphash.MakeSeed(),
		now:       time.Now,
		maxValLen: 512 << 20, // 512 MiB, Redis proto-max-bulk-len default
	}
	for i := range s.shards {
		s.shards[i] = &shard{m: make(map[string]entry)}
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

type shard struct {
	mu sync.RWMutex
	m  map[string]entry
}

type entry struct {
	val      []byte
	expireAt int64 // 0 = no expiry
}

// expEntry is one element in the min-heap used for active TTL expiry.
type expEntry struct {
	deadline int64
	key      string
}

type expHeap []expEntry

func (h expHeap) Len() int           { return len(h) }
func (h expHeap) Less(i, j int) bool { return h[i].deadline < h[j].deadline }
func (h expHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expHeap) Push(x any)        { *h = append(*h, x.(expEntry)) }
func (h *expHeap) Pop() any          { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// ShardCount reports the number of shards (for diagnostics/info).
func (s *Store) ShardCount() int { return len(s.shards) }

// Now returns the store's current time. Used by AOF propagation so absolute
// deadlines recorded in the log match the store's clock.
func (s *Store) Now() time.Time { return s.now() }

// shardForBytes returns the shard owning a []byte key using maphash.Bytes
// (faster than string conversion + maphash.String).
func (s *Store) shardForBytes(key []byte) *shard {
	return s.shards[maphash.Bytes(s.seed, key)&uint64(len(s.shards)-1)]
}

// shardIndex returns the index of the shard owning a []byte key.
func (s *Store) shardIndex(key []byte) int {
	return int(maphash.Bytes(s.seed, key) & uint64(len(s.shards)-1))
}

// shardForString returns the shard owning a string key (used by the expiry
// sweeper which iterates heap entries that are already strings).
func (s *Store) shardForString(key string) *shard {
	return s.shards[maphash.String(s.seed, key)&uint64(len(s.shards)-1)]
}

// expired reports whether e has passed its deadline. Callers must hold the
// owning shard's lock.
func (s *Store) expired(e *entry) bool {
	return e.expireAt != 0 && s.now().UnixNano() >= e.expireAt
}

// purge deletes key from shard and decrements the live count. Must be called
// while holding sh's lock.
func (s *Store) purge(sh *shard, key string) {
	delete(sh.m, key)
	s.count.Add(-1)
}

// Get returns the value for key and whether it exists. Expired entries are
// treated as absent and purged. The returned slice is immutable.
func (s *Store) Get(key []byte) ([]byte, bool) {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)] // compiler elides the string allocation
	if !ok {
		return nil, false
	}
	if s.expired(&e) {
		s.purge(sh, string(key))
		return nil, false
	}
	return e.val, true
}

// SetMode controls the write condition for Set.
type SetMode uint8

const (
	SetAlways SetMode = iota
	SetNX             // only if the key does not already exist
	SetXX             // only if the key already exists
)

// Set stores val under key with an optional ttl (<= 0 disables expiry) and
// reports whether the write happened (false when an NX/XX condition fails).
func (s *Store) Set(key []byte, val []byte, ttl time.Duration, mode SetMode) bool {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, exists := sh.m[string(key)]
	if exists && s.expired(&e) {
		s.purge(sh, string(key))
		exists = false
	}
	switch mode {
	case SetNX:
		if exists {
			return false
		}
	case SetXX:
		if !exists {
			return false
		}
	}
	var dl int64
	if ttl > 0 {
		dl = s.now().Add(ttl).UnixNano()
	}
	sh.m[string(key)] = entry{val: val, expireAt: dl}
	if !exists {
		s.count.Add(1)
	}
	if dl != 0 {
		// pushExpiry takes string; this conversion is rare (only TTL writes).
		s.pushExpiry(string(key), dl)
	}
	return true
}

// Del removes keys and reports how many existed (and were not already
// expired).
func (s *Store) Del(keys ...[]byte) int {
	n := 0
	for _, k := range keys {
		sh := s.shardForBytes(k)
		sh.mu.Lock()
		e, ok := sh.m[string(k)]
		if ok {
			if !s.expired(&e) {
				n++
			}
			s.purge(sh, string(k))
		}
		sh.mu.Unlock()
	}
	return n
}

// Exists reports how many of the keys exist and are not expired.
func (s *Store) Exists(keys ...[]byte) int {
	n := 0
	for _, k := range keys {
		sh := s.shardForBytes(k)
		sh.mu.RLock()
		e, ok := sh.m[string(k)]
		if ok && !s.expired(&e) {
			n++
		}
		sh.mu.RUnlock()
	}
	return n
}

// IncrBy atomically adds delta to the integer stored at key (0 if missing)
// and returns the new value.
func (s *Store) IncrBy(key []byte, delta int64) (int64, error) {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if ok && s.expired(&e) {
		s.purge(sh, string(key))
		ok = false
	}
	var cur int64
	if ok {
		n, err := strconv.ParseInt(string(e.val), 10, 64)
		if err != nil {
			return 0, ErrNotInteger
		}
		cur = n
	}
	cur += delta
	sh.m[string(key)] = entry{val: strconv.AppendInt(nil, cur, 10)}
	if !ok {
		s.count.Add(1)
	}
	return cur, nil
}

// Append appends suffix to the value at key (creating it if missing) and
// returns the new length.
func (s *Store) Append(key, suffix []byte) (int, error) {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if ok && s.expired(&e) {
		s.purge(sh, string(key))
		ok = false
	}
	if !ok {
		if int64(len(suffix)) > s.maxValLen {
			return 0, ErrValueTooLarge
		}
		sh.m[string(key)] = entry{val: append([]byte(nil), suffix...)}
		s.count.Add(1)
		return len(suffix), nil
	}
	if int64(len(e.val))+int64(len(suffix)) > s.maxValLen {
		return 0, ErrValueTooLarge
	}
	nv := make([]byte, 0, len(e.val)+len(suffix))
	nv = append(nv, e.val...)
	nv = append(nv, suffix...)
	sh.m[string(key)] = entry{val: nv, expireAt: e.expireAt}
	return len(nv), nil
}

// Expire sets a TTL on key. A non-positive ttl deletes the key. Returns true
// if the key existed.
func (s *Store) Expire(key []byte, ttl time.Duration) bool {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if !ok || s.expired(&e) {
		if ok {
			s.purge(sh, string(key))
		}
		return false
	}
	if ttl <= 0 {
		s.purge(sh, string(key))
		return true
	}
	dl := s.now().Add(ttl).UnixNano()
	sh.m[string(key)] = entry{val: e.val, expireAt: dl}
	s.pushExpiry(string(key), dl)
	return true
}

// TTL returns the remaining time to live. The bool is false when the key is
// missing and a negative duration when present without a TTL.
func (s *Store) TTL(key []byte) (time.Duration, bool) {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if !ok {
		return 0, false
	}
	if s.expired(&e) {
		s.purge(sh, string(key))
		return 0, false
	}
	if e.expireAt == 0 {
		return -1, true
	}
	return time.Duration(e.expireAt - s.now().UnixNano()), true
}

// Type reports the value type at key: "string" when present, "none" when
// missing.
func (s *Store) Type(key []byte) string {
	if _, ok := s.Get(key); ok {
		return "string"
	}
	return "none"
}

// Flush removes all keys.
func (s *Store) Flush() {
	// Lock every shard so no writer can interleave between the map clears and
	// the live-count reset (otherwise a concurrent Set could leave the count
	// desynced from the maps). Lock order is shard-index ascending and each
	// writer takes exactly one shard, so this cannot deadlock.
	for _, sh := range s.shards {
		sh.mu.Lock()
	}
	for _, sh := range s.shards {
		clear(sh.m)
	}
	s.count.Store(0)
	for i := len(s.shards) - 1; i >= 0; i-- {
		s.shards[i].mu.Unlock()
	}
	s.expMu.Lock()
	s.exp = nil
	s.expMu.Unlock()
}

// Len reports the number of live (unexpired) keys.
func (s *Store) Len() int { return int(s.count.Load()) }

// StartExpiry launches the active expiry sweeper; it runs until ctx is
// cancelled. The sweeper pops every deadline that has passed and purges the
// corresponding keys, but only if they still carry that same deadline (stale
// heap entries for overwritten/deleted keys are filtered out).
func (s *Store) StartExpiry(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep()
			}
		}
	}()
}

func (s *Store) pushExpiry(key string, deadline int64) {
	s.expMu.Lock()
	heap.Push(&s.exp, expEntry{deadline: deadline, key: key})
	s.expMu.Unlock()
}

func (s *Store) sweep() {
	now := s.now().UnixNano()
	// PopN: collect all expired entries under the expiry lock, then purge
	// each under its shard lock (never hold expMu across a shard lock).
	s.expMu.Lock()
	var expired []expEntry
	for s.exp.Len() > 0 && s.exp[0].deadline <= now {
		expired = append(expired, heap.Pop(&s.exp).(expEntry))
	}
	s.expMu.Unlock()

	for _, e := range expired {
		sh := s.shardForString(e.key)
		sh.mu.Lock()
		if cur, ok := sh.m[e.key]; ok && cur.expireAt == e.deadline {
			s.purge(sh, e.key)
		}
		sh.mu.Unlock()
	}
}

// Keys returns every live key whose name matches the glob pattern. It scans
// all shards; O(N) over the dataset, matching Redis KEYS. Pattern semantics
// are those of GlobMatch (Redis glob).
func (s *Store) Keys(pattern string) []string {
	var keys []string
	for _, sh := range s.shards {
		sh.mu.RLock()
		for k, e := range sh.m {
			if s.expired(&e) {
				continue
			}
			if pattern == "" || GlobMatch(pattern, k) {
				keys = append(keys, k)
			}
		}
		sh.mu.RUnlock()
	}
	return keys
}

// Scan implements cursor-based key iteration (Redis SCAN). The cursor is
// opaque to callers: 0 starts a scan and is also returned when iteration is
// complete. count is an advisory hint (the returned batch may exceed it);
// pattern filters keys (empty matches all). Each call fully scans one shard
// so no live key is skipped; cursors advance one shard per call.
//
// Semantics match Redis: a key present for the whole scan is returned at
// least once; a key modified during the scan may be returned more than once.
func (s *Store) Scan(cursor, count int, pattern string) (int, []string) {
	if count <= 0 {
		count = 10
	}
	// cursor is a 1-based shard index; 0 means start (or done).
	start := cursor
	if start <= 0 || start > len(s.shards) {
		start = 1
	}
	idx := start - 1
	sh := s.shards[idx]
	var keys []string
	sh.mu.RLock()
	for k, e := range sh.m {
		if s.expired(&e) {
			continue
		}
		if pattern == "" || GlobMatch(pattern, k) {
			keys = append(keys, k)
		}
	}
	sh.mu.RUnlock()
	if idx+1 >= len(s.shards) {
		return 0, keys // all shards consumed → done
	}
	return idx + 2, keys // next shard (1-based)
}

// GetSet atomically stores val under key and returns the previous value and
// whether the key existed. Following Redis GETSET, any existing TTL is
// cleared by the write.
func (s *Store) GetSet(key, val []byte) ([]byte, bool) {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if !ok || s.expired(&e) {
		if ok {
			s.purge(sh, string(key))
		}
		sh.m[string(key)] = entry{val: val}
		s.count.Add(1)
		return nil, false
	}
	old := e.val
	sh.m[string(key)] = entry{val: val}
	return old, true
}

// SetNXMulti atomically stores all pairs (key,value) only if none of the keys
// already exist, mirroring Redis MSETNX: no write happens at all when any key
// exists. It locks every involved shard in ascending order, so it cannot
// deadlock with single-shard writers or with Flush (which also locks
// ascending).
func (s *Store) SetNXMulti(pairs [][2][]byte) bool {
	if len(pairs) == 0 {
		return true
	}
	// Involved shards, unique, ascending (consistent lock order).
	seen := make(map[int]struct{}, len(pairs))
	var idxs []int
	for _, p := range pairs {
		id := s.shardIndex(p[0])
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			idxs = append(idxs, id)
		}
	}
	sort.Ints(idxs)
	for _, id := range idxs {
		s.shards[id].mu.Lock()
	}
	defer func() {
		for i := len(idxs) - 1; i >= 0; i-- {
			s.shards[idxs[i]].mu.Unlock()
		}
	}()

	// Any existing key (including one already expired → purged) aborts all.
	for _, p := range pairs {
		sh := s.shards[s.shardIndex(p[0])]
		if e, ok := sh.m[string(p[0])]; ok {
			if s.expired(&e) {
				s.purge(sh, string(p[0]))
				continue
			}
			return false
		}
	}
	for _, p := range pairs {
		sh := s.shards[s.shardIndex(p[0])]
		sh.m[string(p[0])] = entry{val: p[1]}
		s.count.Add(1)
	}
	return true
}

// ExpireAt sets an absolute expiry deadline (Unix seconds), mirroring Redis
// EXPIREAT. A deadline in the past deletes the key immediately (returning
// true if it existed). Returns whether the key existed.
func (s *Store) ExpireAt(key []byte, unixSeconds int64) bool {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if !ok || s.expired(&e) {
		if ok {
			s.purge(sh, string(key))
		}
		return false
	}
	dl := unixSeconds * int64(time.Second)
	if dl <= s.now().UnixNano() {
		// Past deadline: Redis deletes the key right away.
		s.purge(sh, string(key))
		return true
	}
	sh.m[string(key)] = entry{val: e.val, expireAt: dl}
	s.pushExpiry(string(key), dl)
	return true
}

// ExpireAtMs sets an absolute expiry deadline in Unix milliseconds, mirroring
// Redis PEXPIREAT. A deadline in the past deletes the key immediately.
// Returns whether the key existed.
func (s *Store) ExpireAtMs(key []byte, unixMs int64) bool {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if !ok || s.expired(&e) {
		if ok {
			s.purge(sh, string(key))
		}
		return false
	}
	dl := unixMs * int64(time.Millisecond)
	if dl <= s.now().UnixNano() {
		s.purge(sh, string(key))
		return true
	}
	sh.m[string(key)] = entry{val: e.val, expireAt: dl}
	s.pushExpiry(string(key), dl)
	return true
}

// Persist removes any TTL from key, mirroring Redis PERSIST. Returns whether
// a timeout was removed (false for a missing key or one with no TTL).
func (s *Store) Persist(key []byte) bool {
	sh := s.shardForBytes(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[string(key)]
	if !ok || s.expired(&e) {
		if ok {
			s.purge(sh, string(key))
		}
		return false
	}
	if e.expireAt == 0 {
		return false // no timeout to remove
	}
	sh.m[string(key)] = entry{val: e.val}
	return true
}
