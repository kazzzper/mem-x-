package store

import (
	"bytes"
	"context"
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
	s.Set("k", []byte("v"), 0, SetAlways)
	got, ok := s.Get("k")
	if !ok || !bytes.Equal(got, []byte("v")) {
		t.Fatalf("got %q, %v", got, ok)
	}
	// overwrite
	s.Set("k", []byte("v2"), 0, SetAlways)
	got, _ = s.Get("k")
	if !bytes.Equal(got, []byte("v2")) {
		t.Fatalf("got %q", got)
	}
}

func TestGetMissing(t *testing.T) {
	s, _ := newTest(t)
	if _, ok := s.Get("nope"); ok {
		t.Fatal("expected missing key")
	}
}

func TestSetNX(t *testing.T) {
	s, _ := newTest(t)
	if !s.Set("k", []byte("v"), 0, SetNX) {
		t.Fatal("NX on missing key should succeed")
	}
	if s.Set("k", []byte("x"), 0, SetNX) {
		t.Fatal("NX on existing key should fail")
	}
	if got, _ := s.Get("k"); !bytes.Equal(got, []byte("v")) {
		t.Fatalf("got %q, want original", got)
	}
	// NX should succeed after expiry
	s.Set("k", []byte("v"), time.Second, SetAlways)
	s.Set("e", []byte("tmp"), 0, SetAlways)
}

func TestSetXX(t *testing.T) {
	s, _ := newTest(t)
	if s.Set("k", []byte("v"), 0, SetXX) {
		t.Fatal("XX on missing key should fail")
	}
	s.Set("k", []byte("v"), 0, SetAlways)
	if !s.Set("k", []byte("x"), 0, SetXX) {
		t.Fatal("XX on existing key should succeed")
	}
	if got, _ := s.Get("k"); !bytes.Equal(got, []byte("x")) {
		t.Fatalf("got %q", got)
	}
}

func TestExpiry(t *testing.T) {
	s, f := newTest(t)
	s.Set("k", []byte("v"), 10*time.Second, SetAlways)
	if got, ok := s.Get("k"); !ok || !bytes.Equal(got, []byte("v")) {
		t.Fatalf("before expiry: got %q, %v", got, ok)
	}
	f.advance(10 * time.Second)
	if _, ok := s.Get("k"); ok {
		t.Fatal("key should be expired")
	}
	if n := s.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0", n)
	}
}

func TestExpiryNX(t *testing.T) {
	s, f := newTest(t)
	s.Set("k", []byte("v"), time.Second, SetAlways)
	f.advance(2 * time.Second)
	if !s.Set("k", []byte("fresh"), 0, SetNX) {
		t.Fatal("NX on expired key should succeed")
	}
}

func TestTTL(t *testing.T) {
	s, f := newTest(t)
	if _, ok := s.TTL("missing"); ok {
		t.Fatal("missing key should report not-exists")
	}
	s.Set("noexp", []byte("v"), 0, SetAlways)
	d, ok := s.TTL("noexp")
	if !ok || d >= 0 {
		t.Fatalf("no-ttl key: d=%v ok=%v", d, ok)
	}
	s.Set("k", []byte("v"), 10*time.Second, SetAlways)
	d, ok = s.TTL("k")
	if !ok || d != 10*time.Second {
		t.Fatalf("ttl key: d=%v ok=%v", d, ok)
	}
	f.advance(5 * time.Second)
	d, _ = s.TTL("k")
	if d != 5*time.Second {
		t.Fatalf("after advance: d=%v", d)
	}
	f.advance(5 * time.Second)
	if _, ok := s.TTL("k"); ok {
		t.Fatal("expired key should report not-exists")
	}
}

func TestExpire(t *testing.T) {
	s, f := newTest(t)
	if s.Expire("missing", time.Second) {
		t.Fatal("expire on missing should return false")
	}
	s.Set("k", []byte("v"), 0, SetAlways)
	if !s.Expire("k", time.Second) {
		t.Fatal("expire on existing should return true")
	}
	f.advance(2 * time.Second)
	if _, ok := s.Get("k"); ok {
		t.Fatal("key should be expired")
	}
}

func TestIncr(t *testing.T) {
	s, _ := newTest(t)
	n, err := s.IncrBy("n", 1)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, _ = s.IncrBy("n", 1)
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
	n, _ = s.IncrBy("n", -3)
	if n != -1 {
		t.Fatalf("n=%d", n)
	}
	s.Set("str", []byte("abc"), 0, SetAlways)
	if _, err := s.IncrBy("str", 1); err != ErrNotInteger {
		t.Fatalf("expected ErrNotInteger, got %v", err)
	}
}

func TestAppend(t *testing.T) {
	s, _ := newTest(t)
	n, err := s.Append("k", []byte("a"))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, _ = s.Append("k", []byte("bc"))
	if n != 3 {
		t.Fatalf("n=%d", n)
	}
	if got, _ := s.Get("k"); !bytes.Equal(got, []byte("abc")) {
		t.Fatalf("got %q", got)
	}
}

func TestAppendTooLarge(t *testing.T) {
	s, _ := newTest(t)
	// Override maxValLen to a small value — WithMaxValueLen is not used here
	// because newTest uses WithShards only; set it directly.
	s.maxValLen = 4
	_, err := s.Append("k", []byte("toolong"))
	if err != ErrValueTooLarge {
		t.Fatalf("expected ErrValueTooLarge, got %v", err)
	}
	// A small append should still work.
	s.maxValLen = 512 << 20
	n, err := s.Append("k", []byte("abc"))
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Append that would exceed the cap on the existing value ("abc" + "xy").
	s.maxValLen = 4
	_, err = s.Append("k", []byte("xy"))
	if err != ErrValueTooLarge {
		t.Fatalf("expected ErrValueTooLarge, got %v", err)
	}
}

func TestDel(t *testing.T) {
	s, f := newTest(t)
	s.Set("a", []byte("1"), 0, SetAlways)
	s.Set("b", []byte("2"), 0, SetAlways)
	s.Set("c", []byte("3"), time.Second, SetAlways)
	f.advance(2 * time.Second) // c expires
	if n := s.Del("a", "c", "missing"); n != 1 {
		t.Fatalf("Del = %d, want 1", n)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("a should be gone")
	}
	if n := s.Len(); n != 1 {
		t.Fatalf("Len = %d, want 1", n)
	}
}

func TestExists(t *testing.T) {
	s, f := newTest(t)
	s.Set("a", []byte("1"), 0, SetAlways)
	s.Set("b", []byte("2"), time.Second, SetAlways)
	f.advance(2 * time.Second) // b expires
	if n := s.Exists("a", "b", "c"); n != 1 {
		t.Fatalf("Exists = %d, want 1", n)
	}
}

func TestType(t *testing.T) {
	s, _ := newTest(t)
	s.Set("k", []byte("v"), 0, SetAlways)
	if got := s.Type("k"); got != "string" {
		t.Fatalf("got %q", got)
	}
	if got := s.Type("missing"); got != "none" {
		t.Fatalf("got %q", got)
	}
}

func TestFlush(t *testing.T) {
	s, _ := newTest(t)
	s.Set("a", []byte("1"), 0, SetAlways)
	s.Set("b", []byte("2"), 0, SetAlways)
	s.Flush()
	if n := s.Len(); n != 0 {
		t.Fatalf("Len = %d, want 0", n)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("a should be gone")
	}
}

func TestSweep(t *testing.T) {
	s, f := newTest(t)
	s.Set("exp", []byte("1"), 2*time.Second, SetAlways)
	s.Set("keep", []byte("2"), 0, SetAlways)
	// Overwrite exp before its deadline: the old heap entry must not delete it.
	f.advance(1 * time.Second)
	s.Set("exp", []byte("new"), 10*time.Second, SetAlways)
	f.advance(2 * time.Second) // old deadline passes
	s.sweep()
	if _, ok := s.Get("exp"); !ok {
		t.Fatal("exp should still exist (old stale heap entry filtered)")
	}
	f.advance(9 * time.Second) // new deadline passes
	s.sweep()
	if _, ok := s.Get("exp"); ok {
		t.Fatal("exp should be swept now")
	}
	if _, ok := s.Get("keep"); !ok {
		t.Fatal("keep should still exist")
	}
}

func TestStartExpiry(t *testing.T) {
	s, f := newTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartExpiry(ctx, time.Millisecond)
	s.Set("k", []byte("v"), 1*time.Second, SetAlways)
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
			key := "key" + strconv.Itoa(g)
			for i := 0; i < ops; i++ {
				s.Set(key, []byte("v"), 0, SetAlways)
				if _, ok := s.Get(key); !ok {
					t.Errorf("get miss for %s", key)
					return
				}
				s.IncrBy("counter"+strconv.Itoa(g%4), 1)
				if i%50 == 0 {
					s.Del(key)
				}
			}
		}(g)
	}
	wg.Wait()
}
