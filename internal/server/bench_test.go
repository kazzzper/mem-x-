package server

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"

	"mem-x/internal/command"
	"mem-x/internal/config"
	"mem-x/internal/resp"
	"mem-x/internal/store"
)

// newBenchServer returns a Server wired like production (pools initialized).
func newBenchServer() *Server {
	cfg := config.Config{MaxConn: 100}
	st := store.New()
	reg := command.New(st, command.NewStats())
	stats := command.NewStats()
	return New(cfg, st, reg, stats)
}

// BenchmarkConnBufioNew measures the per-connection setup cost WITHOUT
// pooling: each iteration allocates a fresh 16 KiB reader and writer buffer
// pair.
func BenchmarkConnBufioNew(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		br := bufio.NewReaderSize(strings.NewReader("PING\r\n"), 16<<10)
		bw := resp.NewWriter(io.Discard)
		_ = br
		_ = bw
	}
}

// BenchmarkConnBufioPooled measures the same setup using the Server's
// sync.Pool: buffers are fetched, reset, and returned instead of re-allocated.
func BenchmarkConnBufioPooled(b *testing.B) {
	s := newBenchServer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		br := s.readerPool.Get().(*bufio.Reader)
		br.Reset(strings.NewReader("PING\r\n"))
		bw := s.writerPool.Get().(*resp.Writer)
		bw.Reset(io.Discard)
		_ = br
		_ = bw
		s.readerPool.Put(br)
		s.writerPool.Put(bw)
	}
}

// BenchmarkConnBufioPooledResetOnly isolates the Reset cost for a reused
// buffer, which is what a long-lived server experiences per new connection.
func BenchmarkConnBufioPooledResetOnly(b *testing.B) {
	s := newBenchServer()
	br := s.readerPool.Get().(*bufio.Reader)
	bw := s.writerPool.Get().(*resp.Writer)
	defer func() {
		s.readerPool.Put(br)
		s.writerPool.Put(bw)
	}()
	client, srv := net.Pipe()
	defer client.Close()
	defer srv.Close()
	br.Reset(srv)
	bw.Reset(srv)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		br.Reset(srv)
		bw.Reset(srv)
	}
	_ = br
	_ = bw
}
