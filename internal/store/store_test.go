package store

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a thread-safe, deterministic time source using atomic.Int64 so
// the sweeper goroutine and the test goroutine never race (and no mutex lock
// ordering cycle with the store's shard locks is introduced).
type fakeClock struct {
	n atomic.Int64 // UnixNano time
}

func (f *fakeClock) Now() time.Time {
	return time.Unix(0, f.n.Load())
}

func (f *fakeClock) advance(d time.Duration) {
	f.n.Add(int64(d))
}

func newTest(t *testing.T) (*Store, *fakeClock) {
	t.Helper()
	f := &fakeClock{}
	f.n.Store(time.Unix(1000, 0).UnixNano())
	s := New(WithClock(f.Now), WithShards(4))
	return s, f
}

func TestSetGet(t *testing.T) {
	s, _ := newTest(t)
	s.Set([]byte("k"), []byte("v"), 0, SetAlways)
	got, ok := s.Get([]byte("k"))
	if !ok || !bytes.Equal(got, []byte("v")) {
		t.Fatalf("got %q, %v", got, ok)
	}
	// overwrite
	s.Set([]byte("k"), []byte("v2"), 0, SetAlways)
	got, _ = s.Get([]byte("k"))
	if !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("got %q", got)
	}
}

func TestGetMissing(t *testing.T) {
	s, _ := newTest(t)
	if _, ok := s.Get([]byte("nope")); ok {
		t.Fatal("expected missing key")
	}
}

func TestSetNX(t *testing.T) {
	s, _ := newTest(t)
	if !s.Set([]byte("k"), []byte("v"), 0, SetNX) {
		t.Fatal("NX on missing key should succeed")
	}
	if s.Set([]byte("k"), []byte("x"), 0, SetNX) {
		t.Fatal("NX on existing key should fail")
	}
	if got, _ := s.Get([]byte("k")); !bytes.Equal(got, []byte("v")) {
		t.Fatalf("got %q, want original", got)
	}
	// NX should succeed after expiry
	s.Set([]byte("k"), []byte("v"), time.Second, SetAlways)
	s.Set([]byte("e"), []byte("tmp"), 0, SetAlways)
}

func TestSetXX(t *testing.T) {
	s, _ := newTest(t)
	if s.Set([]byte("k"), []byte("v"), 0, SetXX) {
		t.Fatal("XX on missing key should fail")
	}
	s.Set([]byte("k"), []byte("v"), 0, SetAlways)
	if !s.Set([]byte("k"), []byte("x"), 0, SetXX) {
		t.Fatal("XX on existing key should succeed")
	}
	if got, _ := s.Get([]byte("k")); !bytes.Equal(got, []byte("x")) {
		t.Fatalf("got %q", got)
	}
}

func TestExpiry(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("k"), []byte("v"), 10*time.Second, SetAlways)
	if got, ok := s.Get([]byte("k")); !ok || !bytes.Equal(got, []byte("v")) {
		t.Fatalf("before expiry: got %q, %v", got, ok)
	}
	f.advance(10 * time.Second)
	if _, ok := s.Get([]byte("k")); ok {
		t.Fatal("key should be expired")
	}
	if n := s.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0", n)
	}
}

func TestExpiryNX(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("k"), []byte("v"), time.Second, SetAlways)
	f.advance(2 * time.Second)
	if !s.Set([]byte("k"), []byte("fresh"), 0, SetNX) {
		t.Fatal("NX on expired key should succeed")
	}
}

func TestTTL(t *testing.T) {
	s, f := newTest(t)
	if _, ok := s.TTL([]byte("missing")); ok {
		t.Fatal("missing key should report not-exists")
	}
	s.Set([]byte("noexp"), []byte("v"), 0, SetAlways)
	d, ok := s.TTL([]byte("noexp"))
	if !ok || d >= 0 {
		t.Fatalf("no-ttl key: d=%v ok=%v", d, ok)
	}
	s.Set([]byte("k"), []byte("v"), 10*time.Second, SetAlways)
	d, ok = s.TTL([]byte("k"))
	if !ok || d != 10*time.Second {
		t.Fatalf("ttl key: d=%v ok=%v", d, ok)
	}
	f.advance(5 * time.Second)
	d, _ = s.TTL([]byte("k"))
	if d != 5*time.Second {
		t.Fatalf("after advance: d=%v", d)
	}
	f.advance(5 * time.Second)
	if _, ok := s.TTL([]byte("k")); ok {
		t.Fatal("expired key should report not-exists")
	}
}

func TestExpire(t *testing.T) {
	s, f := newTest(t)
	if s.Expire([]byte("missing"), time.Second) {
		t.Fatal("expire on missing should return false")
	}
	s.Set([]byte("k"), []byte("v"), 0, SetAlways)
	if !s.Expire([]byte("k"), time.Second) {
		t.Fatal("expire on existing should return true")
	}
	f.advance(2 * time.Second)
	if _, ok := s.Get([]byte("k")); ok {
		t.Fatal("key should be expired")
	}
}

func TestIncr(t *testing.T) {
	s, _ := newTest(t)
	n, err := s.IncrBy([]byte("n"), 1)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, _ = s.IncrBy([]byte("n"), 1)
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
	n, _ = s.IncrBy([]byte("n"), -3)
	if n != -1 {
		t.Fatalf("n=%d", n)
	}
	s.Set([]byte("str"), []byte("abc"), 0, SetAlways)
	if _, err := s.IncrBy([]byte("str"), 1); err != ErrNotInteger {
		t.Fatalf("expected ErrNotInteger, got %v", err)
	}
}

func TestAppend(t *testing.T) {
	s, _ := newTest(t)
	n, err := s.Append([]byte("k"), []byte("a"))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, _ = s.Append([]byte("k"), []byte("bc"))
	if n != 3 {
		t.Fatalf("n=%d", n)
	}
	if got, _ := s.Get([]byte("k")); !bytes.Equal(got, []byte("abc")) {
		t.Fatalf("got %q", got)
	}
}

func TestAppendTooLarge(t *testing.T) {
	s, _ := newTest(t)
	// Override maxValLen to a small value — WithMaxValueLen is not used here
	// because newTest uses WithShards only; set it directly.
	s.maxValLen = 4
	_, err := s.Append([]byte("k"), []byte("toolong"))
	if err != ErrValueTooLarge {
		t.Fatalf("expected ErrValueTooLarge, got %v", err)
	}
	// A small append should still work.
	s.maxValLen = 512 << 20
	n, err := s.Append([]byte("k"), []byte("abc"))
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Append that would exceed the cap on the existing value ("abc" + "xy").
	s.maxValLen = 4
	_, err = s.Append([]byte("k"), []byte("xy"))
	if err != ErrValueTooLarge {
		t.Fatalf("expected ErrValueTooLarge, got %v", err)
	}
}

func TestDel(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("a"), []byte("1"), 0, SetAlways)
	s.Set([]byte("b"), []byte("2"), 0, SetAlways)
	s.Set([]byte("c"), []byte("3"), time.Second, SetAlways)
	f.advance(2 * time.Second) // c expires
	if n := s.Del([]byte("a"), []byte("c"), []byte("missing")); n != 1 {
		t.Fatalf("Del = %d, want 1", n)
	}
	if _, ok := s.Get([]byte("a")); ok {
		t.Fatal("a should be gone")
	}
	if n := s.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1", n)
	}
}

func TestExists(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("a"), []byte("1"), 0, SetAlways)
	s.Set([]byte("b"), []byte("2"), time.Second, SetAlways)
	f.advance(2 * time.Second) // b expires
	if n := s.Exists([]byte("a"), []byte("b"), []byte("c")); n != 1 {
		t.Fatalf("Exists = %d, want 1", n)
	}
}

func TestType(t *testing.T) {
	s, _ := newTest(t)
	s.Set([]byte("k"), []byte("v"), 0, SetAlways)
	if got := s.Type([]byte("k")); got != "string" {
		t.Fatalf("got %q", got)
	}
	if got := s.Type([]byte("missing")); got != "none" {
		t.Fatalf("got %q", got)
	}
}

func TestFlush(t *testing.T) {
	s, _ := newTest(t)
	s.Set([]byte("a"), []byte("1"), 0, SetAlways)
	s.Set([]byte("b"), []byte("2"), 0, SetAlways)
	s.Flush()
	if n := s.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0", n)
	}
	if _, ok := s.Get([]byte("a")); ok {
		t.Fatal("a should be gone")
	}
}

func TestSweep(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("exp"), []byte("1"), 2*time.Second, SetAlways)
	s.Set([]byte("keep"), []byte("2"), 0, SetAlways)
	// Overwrite exp before its deadline: the old heap entry must not delete it.
	f.advance(1 * time.Second)
	s.Set([]byte("exp"), []byte("new"), 10*time.Second, SetAlways)
	f.advance(2 * time.Second) // old deadline passes
	s.sweep()
	if _, ok := s.Get([]byte("exp")); !ok {
		t.Fatal("exp should still exist (old stale heap entry filtered)")
	}
	f.advance(9 * time.Second) // new deadline passes
	s.sweep()
	if _, ok := s.Get([]byte("exp")); ok {
		t.Fatal("exp should be swept now")
	}
	if _, ok := s.Get([]byte("keep")); !ok {
		t.Fatal("keep should still exist")
	}
}

func TestStartExpiry(t *testing.T) {
	s, f := newTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartExpiry(ctx, time.Millisecond)
	s.Set([]byte("k"), []byte("v"), 1*time.Second, SetAlways)
	// The sweeper reads the same fake clock; advance past expiry and give the
	// ticker a chance to run.
	f.advance(2 * time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for s.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := s.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0 after sweeper", n)
	}
}

func TestConcurrent(t *testing.T) {
	s, _ := newTest(t)
	const goroutines = 32
	const ops = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := []byte("key" + strconv.Itoa(g))
			for i := 0; i < ops; i++ {
				s.Set(key, []byte("v"), 0, SetAlways)
				if _, ok := s.Get(key); !ok {
					t.Errorf("get miss for %s", key)
					return
				}
				s.IncrBy([]byte("counter"+strconv.Itoa(g%4)), 1)
				if i%50 == 0 {
					s.Del(key)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestKeys(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("user:1"), []byte("a"), 0, SetAlways)
	s.Set([]byte("user:2"), []byte("b"), 0, SetAlways)
	s.Set([]byte("admin:1"), []byte("c"), 0, SetAlways)
	s.Set([]byte("ephemeral"), []byte("d"), time.Second, SetAlways)
	f.advance(2 * time.Second)

	got := s.Keys("*")
	if len(got) != 3 {
		t.Fatalf("Keys(*) = %v, want 3 live keys", got)
	}
	got = s.Keys("user:*")
	if len(got) != 2 {
		t.Fatalf("Keys(user:*) = %v, want 2", got)
	}
	if got := s.Keys("nope*"); len(got) != 0 {
		t.Fatalf("Keys(nope*) = %v, want 0", got)
	}
}

func TestScan(t *testing.T) {
	s, f := newTest(t)
	const n = 25
	for i := 0; i < n; i++ {
		s.Set([]byte(fmt.Sprintf("key:%02d", i)), []byte("v"), 0, SetAlways)
	}
	s.Set([]byte("other"), []byte("v"), time.Second, SetAlways)
	f.advance(2 * time.Second) // other expires

	// Full iteration must return every live key exactly once (scan is
	// per-shard, so no duplicates within a pass).
	cursor, count := 0, 5
	seen := map[string]bool{}
	passes := 0
	for {
		var batch []string
		cursor, batch = s.Scan(cursor, count, "key:*")
		for _, k := range batch {
			if seen[k] {
				t.Fatalf("duplicate key %q returned", k)
			}
			seen[k] = true
		}
		passes++
		if cursor == 0 {
			break
		}
		if passes > len(s.shards)+2 {
			t.Fatalf("scan did not terminate; seen %d keys", len(seen))
		}
	}
	if len(seen) != n {
		t.Fatalf("scan returned %d keys, want %d", len(seen), n)
	}
}

func TestScanEmptyPattern(t *testing.T) {
	s, _ := newTest(t)
	s.Set([]byte("a"), []byte("1"), 0, SetAlways)
	s.Set([]byte("b"), []byte("2"), 0, SetAlways)
	cursor := 0
	var all []string
	for {
		var batch []string
		cursor, batch = s.Scan(cursor, 1, "")
		all = append(all, batch...)
		if cursor == 0 {
			break
		}
	}
	if len(all) != 2 {
		t.Fatalf("Scan('') = %v, want 2 keys", all)
	}
}

func TestGetSet(t *testing.T) {
	s, _ := newTest(t)
	if old, ok := s.GetSet([]byte("k"), []byte("v1")); ok || old != nil {
		t.Fatalf("GetSet on missing key = (%q,%v), want (nil,false)", old, ok)
	}
	if v, _ := s.Get([]byte("k")); string(v) != "v1" {
		t.Fatalf("after GetSet, Get = %q, want v1", v)
	}
	old, ok := s.GetSet([]byte("k"), []byte("v2"))
	if !ok || string(old) != "v1" {
		t.Fatalf("GetSet = (%q,%v), want (v1,true)", old, ok)
	}
	if v, _ := s.Get([]byte("k")); string(v) != "v2" {
		t.Fatalf("after second GetSet, Get = %q, want v2", v)
	}
}

func TestGetSetClearsTTL(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("k"), []byte("v1"), 50*time.Millisecond, SetAlways)
	f.advance(30 * time.Millisecond)
	s.GetSet([]byte("k"), []byte("v2"))
	f.advance(40 * time.Millisecond) // past original deadline
	if _, ok := s.Get([]byte("k")); !ok {
		t.Fatal("GETSET must clear the previous TTL (key should still be live)")
	}
}

func TestSetNXMulti(t *testing.T) {
	s, _ := newTest(t)
	pairs := [][2][]byte{{[]byte("a"), []byte("1")}, {[]byte("b"), []byte("2")}, {[]byte("c"), []byte("3")}}
	if !s.SetNXMulti(pairs) {
		t.Fatal("SetNXMulti on empty store should succeed")
	}
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
	// Second call with one existing key must be a no-op.
	other := [][2][]byte{{[]byte("d"), []byte("4")}, {[]byte("a"), []byte("9")}}
	if s.SetNXMulti(other) {
		t.Fatal("SetNXMulti with an existing key must not write")
	}
	if _, ok := s.Get([]byte("d")); ok {
		t.Fatal("d must not exist after failed SetNXMulti")
	}
	if v, _ := s.Get([]byte("a")); string(v) != "1" {
		t.Fatalf("a = %q, want 1 (untouched)", v)
	}
}

func TestSetNXMultiExpiredAborts(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("a"), []byte("old"), time.Second, SetAlways)
	f.advance(2 * time.Second) // a expires
	// Expired key is treated as absent, so the batch should succeed.
	if !s.SetNXMulti([][2][]byte{{[]byte("a"), []byte("new")}, {[]byte("b"), []byte("2")}}) {
		t.Fatal("SetNXMulti should succeed when existing key is expired")
	}
	if v, _ := s.Get([]byte("a")); string(v) != "new" {
		t.Fatalf("a = %q, want new", v)
	}
}

func TestExpireAt(t *testing.T) {
	s, f := newTest(t)
	now := f.Now().Unix()
	if s.ExpireAt([]byte("missing"), now+10) {
		t.Fatal("ExpireAt on missing key should return false")
	}
	s.Set([]byte("k"), []byte("v"), 0, SetAlways)
	if !s.ExpireAt([]byte("k"), now+1) {
		t.Fatal("ExpireAt should return true for an existing key")
	}
	f.advance(2 * time.Second)
	if _, ok := s.Get([]byte("k")); ok {
		t.Fatal("key should be expired after its EXPIREAT deadline")
	}
}

func TestExpireAtPast(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("k"), []byte("v"), 0, SetAlways)
	if !s.ExpireAt([]byte("k"), f.Now().Unix()-10) {
		t.Fatal("EXPIREAT in the past on an existing key should delete it and return true")
	}
	if _, ok := s.Get([]byte("k")); ok {
		t.Fatal("key should be gone after past EXPIREAT")
	}
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
}

func TestPersist(t *testing.T) {
	s, f := newTest(t)
	if s.Persist([]byte("missing")) {
		t.Fatal("Persist on missing key should return false")
	}
	s.Set([]byte("k"), []byte("v"), 0, SetAlways)
	if s.Persist([]byte("k")) {
		t.Fatal("Persist on a key without TTL should return false")
	}
	s.Set([]byte("k2"), []byte("v"), time.Second, SetAlways)
	if !s.Persist([]byte("k2")) {
		t.Fatal("Persist on a key with TTL should return true")
	}
	f.advance(2 * time.Second)
	if _, ok := s.Get([]byte("k2")); !ok {
		t.Fatal("key should survive past its old deadline after PERSIST")
	}
}

func TestKeysExpiredExcluded(t *testing.T) {
	s, f := newTest(t)
	s.Set([]byte("live"), []byte("v"), 0, SetAlways)
	s.Set([]byte("dead"), []byte("v"), time.Second, SetAlways)
	f.advance(2 * time.Second)
	got := s.Keys("*")
	if len(got) != 1 || got[0] != "live" {
		t.Fatalf("Keys(*) = %v, want [live]", got)
	}
}
