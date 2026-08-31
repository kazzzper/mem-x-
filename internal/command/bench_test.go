package command

import (
	"context"
	"testing"

	"mem-x/internal/store"
)

func BenchmarkRegistryExecute(b *testing.B) {
	st := store.New(store.WithShards(8))
	reg := New(st, NewStats())
	ctx := context.Background()
	// Realistic payload — SET with a 64-byte key and 1KB value.
	key := make([]byte, 64)
	val := make([]byte, 1024)
	for i := range key {
		key[i] = byte(i % 127)
		if key[i] < 32 || key[i] > 126 {
			key[i] = 'a'
		}
	}
	for i := range val {
		val[i] = byte(i % 127)
		if val[i] < 32 || val[i] > 126 {
			val[i] = 'A'
		}
	}
	// Warmup: insert the key once so GET hits.
	tokens := [][]byte{[]byte("SET"), key, val}
	reg.Execute(ctx, tokens)

	benchCases := []struct {
		name   string
		tokens [][]byte
	}{
		{"GET", [][]byte{[]byte("GET"), key}},
		{"SET", [][]byte{[]byte("SET"), key, val}},
		{"PING", [][]byte{[]byte("PING")}},
		{"INCR", [][]byte{[]byte("INCR"), []byte("counter")}},
	}
	for _, bc := range benchCases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				reg.Execute(ctx, bc.tokens)
			}
		})
	}
}
