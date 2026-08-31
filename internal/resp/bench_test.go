package resp

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func BenchmarkReadCommand(b *testing.B) {
	lim := DefaultLimits()
	// Realistic 3-arg command: SET key value
	payload := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
	benchCases := []struct {
		name string
		data string
	}{
		{"SET_3arg", "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"},
		{"GET_1arg", "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n"},
		{"PING", "*1\r\n$4\r\nPING\r\n"},
		{"INLINE", "PING\r\n"},
	}
	for _, bc := range benchCases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				br := bufio.NewReader(strings.NewReader(bc.data))
				ReadCommand(br, lim)
			}
		})
	}
	_ = payload
}

func BenchmarkWriteReply(b *testing.B) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	benchCases := []struct {
		name  string
		reply Reply
	}{
		{"SimpleString", SimpleReply("OK")},
		{"Bulk", BulkReply([]byte("value"))},
		{"Integer", IntegerReply(42)},
		{"Error", ErrorReply("ERR something")},
		{"NullBulk", NullReply()},
	}
	for _, bc := range benchCases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.Reset()
				bc.reply.WriteTo(w)
				w.Flush()
			}
		})
	}
}
