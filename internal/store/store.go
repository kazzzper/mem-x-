// Package store implements the in-memory key/value engine behind mem-x.
//
// Design (see PLAN.md §3): a fixed set of shards each guarding a slice of the
// key space with an RWMutex, so concurrent access to different keys does not
// contend on one lock. Expiration uses a min-heap of deadlines with lazy
// checks on access plus an active sweeper.
//
// Correctness first: every per-key mutation happens under that key's shard
// lock, and no lock is held across I/O. Stored values are immutable once
// written — callers must not mutate the byte slices returned by Get.
package store

import (
	"container/heap"
	"context"
	"errors"
	"hash/maphash"
	"runtime"
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

// entry is one stored value plus its optional absolute expiration deadline.
type entry struct {
	val      []byte
	expireAt int64 // UnixNano deadline; 0 means no expiry
}

// shard guards a slice of the key space.
type shard struct {
	mu sync.RWMutex
	m  map[string]entry
}

// Option configures a Store.
type Option func(*Store)

// WithClock overrides the Store's time source (for deterministic tests).
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
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

// shardFor returns the shard owning key. Shards are always a power of two, so
// masking is safe. maphash (seeded per Store) spreads keys and thwarts
// hash-flooding adversaries at the shard level.
func (s *Store) shardFor(key string) *shard {
	h := maphash.String(s.seed, key)
	return s.shards[h&uint64(len(s.shards)-1)]
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
func (s *Store) Get(key string) ([]byte, bool) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok {
		return nil, false
	}
	if s.expired(&e) {
		s.purge(sh, key)
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
func (s *Store) Set(key string, val []byte, ttl time.Duration, mode SetMode) bool {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, exists := sh.m[key]
	if exists && s.expired(&e) {
		s.purge(sh, key)
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
	sh.m[key] = entry{val: val, expireAt: dl}
	if !exists {
		s.count.Add(1)
	}
	if dl != 0 {
		s.pushExpiry(key, dl)
	}
	return true
}

// Del removes keys and reports how many existed (and were not already
// expired).
func (s *Store) Del(keys ...string) int {
	n := 0
	for _, k := range keys {
		sh := s.shardFor(k)
		sh.mu.Lock()
		e, ok := sh.m[k]
		if ok {
			if !s.expired(&e) {
				n++
			}
			s.purge(sh, k)
		}
		sh.mu.Unlock()
	}
	return n
}

// Exists reports how many of the keys exist and are not expired.
func (s *Store) Exists(keys ...string) int {
	n := 0
	for _, k := range keys {
		sh := s.shardFor(k)
		sh.mu.RLock()
		e, ok := sh.m[k]
		if ok && !s.expired(&e) {
			n++
		}
		sh.mu.RUnlock()
	}
	return n
}

// IncrBy atomically adds delta to the integer stored at key (0 if missing)
// and returns the new value.
func (s *Store) IncrBy(key string, delta int64) (int64, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[key]
	if ok && s.expired(&e) {
		s.purge(sh, key)
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
	sh.m[key] = entry{val: strconv.AppendInt(nil, cur, 10)}
	if !ok {
		s.count.Add(1)
	}
	return cur, nil
}

// Append appends suffix to the value at key (treating a missing key as an
// empty string) and returns the new length.
func (s *Store) Append(key string, suffix []byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[key]
	if ok && s.expired(&e) {
		s.purge(sh, key)
		ok = false
	}
	if !ok {
		if int64(len(suffix)) > s.maxValLen {
			return 0, ErrValueTooLarge
		}
		sh.m[key] = entry{val: append([]byte(nil), suffix...)}
		s.count.Add(1)
		return len(suffix), nil
	}
	if int64(len(e.val))+int64(len(suffix)) > s.maxValLen {
		return 0, ErrValueTooLarge
	}
	// Build a fresh slice: stored values are immutable, so never mutate e.val.
	nv := make([]byte, 0, len(e.val)+len(suffix))
	nv = append(nv, e.val...)
	nv = append(nv, suffix...)
	sh.m[key] = entry{val: nv, expireAt: e.expireAt}
	return len(nv), nil
}

// Expire sets a ttl on key and reports whether the key existed (and was not
// already expired).
func (s *Store) Expire(key string, ttl time.Duration) bool {
	if ttl <= 0 {
		return false
	}
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[key]
	if !ok {
		return false
	}
	if s.expired(&e) {
		s.purge(sh, key)
		return false
	}
	e.expireAt = s.now().Add(ttl).UnixNano()
	sh.m[key] = e
	s.pushExpiry(key, e.expireAt)
	return true
}

// TTL reports the remaining lifetime for key. It returns (0, false) when the
// key is missing and a negative duration when present without a TTL.
func (s *Store) TTL(key string) (time.Duration, bool) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	e, ok := sh.m[key]
	if !ok {
		return 0, false
	}
	if s.expired(&e) {
		s.purge(sh, key)
		return 0, false
	}
	if e.expireAt == 0 {
		return -1, true
	}
	return time.Duration(e.expireAt - s.now().UnixNano()), true
}

// Type reports the value type at key: "string" when present, "none" when
// missing.
func (s *Store) Type(key string) string {
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

	s.expMu.Lock()
	var keys []string
	for s.exp.Len() > 0 && s.exp[0].deadline <= now {
		keys = append(keys, heap.Pop(&s.exp).(expEntry).key)
	}
	s.expMu.Unlock()

	for _, k := range keys {
		sh := s.shardFor(k)
		sh.mu.Lock()
		if e, ok := sh.m[k]; ok && e.expireAt != 0 && e.expireAt <= now {
			s.purge(sh, k)
		}
		sh.mu.Unlock()
	}
}

// expEntry is one scheduled expiration.
type expEntry struct {
	deadline int64
	key      string
}

// expHeap is a min-heap of expEntry ordered by deadline.
type expHeap []expEntry

func (h expHeap) Len() int           { return len(h) }
func (h expHeap) Less(i, j int) bool { return h[i].deadline < h[j].deadline }
func (h expHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expHeap) Push(x any)        { *h = append(*h, x.(expEntry)) }
func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}
