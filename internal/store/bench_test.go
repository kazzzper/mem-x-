package store

import (
	"fmt"
	"testing"
)

// workingSet returns a fixed slice of distinct byte keys (stable, no per-iter alloc).
func workingSet(n int) [][]byte {
	keys := make([][]byte, n)
	for i := range keys {
		keys[i] = []byte(fmt.Sprintf("key-%04d", i))
	}
	return keys
}

func BenchmarkStoreGet(b *testing.B) {
	s := New(WithShards(8))
	keys := workingSet(1024)
	for _, k := range keys {
		s.Set(k, []byte("value"), 0, SetAlways)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Get(keys[i%len(keys)])
	}
}

func BenchmarkStoreSet(b *testing.B) {
	s := New(WithShards(8))
	keys := workingSet(1024)
	val := make([]byte, 1024)
	for i := range val {
		val[i] = 'A'
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Set(keys[i%len(keys)], val, 0, SetAlways)
	}
}

func BenchmarkStoreIncr(b *testing.B) {
	s := New(WithShards(8))
	keys := workingSet(1024)
	for _, k := range keys {
		s.Set(k, []byte("0"), 0, SetAlways)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.IncrBy(keys[i%len(keys)], 1)
	}
}

func BenchmarkStoreDel(b *testing.B) {
	s := New(WithShards(8))
	keys := workingSet(1024)
	for _, k := range keys {
		s.Set(k, []byte("v"), 0, SetAlways)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Del(keys[i%len(keys)])
	}
}
