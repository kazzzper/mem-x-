package resp

import (
	"bufio"
	"strings"
	"testing"
)

// FuzzReadCommand ensures the parser never panics on arbitrary input and never
// allocates beyond the caps. Run with:
//
//	go test -fuzz=FuzzReadCommand ./internal/resp
func FuzzReadCommand(f *testing.F) {
	seeds := []string{
		"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n",
		"+OK\r\n",
		"-ERR\r\n",
		":42\r\n",
		"$-1\r\n",
		"$5\r\nhello\r\n",
		"PING\r\n",
		"SET k v\r\n",
		"\r\n",
		"*0\r\n",
		"*-1\r\n",
		"*abc\r\n",
		"*1\r\n+foo\r\n",
		"*1\r\n$abc\r\n",
		"*999999999\r\n",
		"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	lim := DefaultLimits()
	f.Fuzz(func(t *testing.T, data string) {
		// No panic is the invariant; errors are expected for most inputs.
		_, _ = ReadCommand(bufio.NewReader(strings.NewReader(data)), lim)
	})
}
