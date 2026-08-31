package resp

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func br(data string) *bufio.Reader {
	return bufio.NewReader(bytes.NewBufferString(data))
}

func eqSlices(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d\n  got=%q\n  want=%q", len(got), len(want), got, want)
	}
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("elem %d: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestReadCommandRESP(t *testing.T) {
	lim := DefaultLimits()
	got, err := ReadCommand(br("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n"), lim)
	if err != nil {
		t.Fatal(err)
	}
	eqSlices(t, got, [][]byte{[]byte("SET"), []byte("k"), []byte("v")})
}

func TestReadCommandRESPEmptyString(t *testing.T) {
	lim := DefaultLimits()
	got, err := ReadCommand(br("*3\r\n$3\r\nSET\r\n$0\r\n\r\n$1\r\nv\r\n"), lim)
	if err != nil {
		t.Fatal(err)
	}
	eqSlices(t, got, [][]byte{[]byte("SET"), []byte(""), []byte("v")})
}

func TestReadCommandInline(t *testing.T) {
	lim := DefaultLimits()
	got, err := ReadCommand(br("PING\r\n"), lim)
	if err != nil {
		t.Fatal(err)
	}
	eqSlices(t, got, [][]byte{[]byte("PING")})

	got2, err := ReadCommand(br("SET k v\r\n"), lim)
	if err != nil {
		t.Fatal(err)
	}
	eqSlices(t, got2, [][]byte{[]byte("SET"), []byte("k"), []byte("v")})
}

func TestReadCommandInlineTabs(t *testing.T) {
	lim := DefaultLimits()
	got, err := ReadCommand(br("SET\tk  v\r\n"), lim)
	if err != nil {
		t.Fatal(err)
	}
	eqSlices(t, got, [][]byte{[]byte("SET"), []byte("k"), []byte("v")})
}

func TestReadCommandBulkLimit(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxBulkLen = 10
	// bulk string of length 100 → rejected
	big := "*2\r\n$3\r\nGET\r\n$100\r\n" + string(make([]byte, 100)) + "\r\n"
	_, err := ReadCommand(br(big), lim)
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ProtocolError, got %T: %v", err, err)
	}
	if pe.Msg != "invalid bulk length" {
		t.Fatalf("got msg %q", pe.Msg)
	}
}

func TestReadCommandArgsLimit(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxArgs = 2
	_, err := ReadCommand(br("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n"), lim)
	if err == nil {
		t.Fatal("expected error for args > limit")
	}
}

func TestReadCommandInvalidMultibulk(t *testing.T) {
	lim := DefaultLimits()
	tests := []string{
		"*abc\r\n",
		"*0\r\n",
		"*-1\r\n",
		"*999999999\r\n", // > MaxArgs
	}
	for _, s := range tests {
		if _, err := ReadCommand(br(s), lim); err == nil {
			t.Fatalf("expected error for %q", s)
		}
	}
}

func TestReadCommandNotBulk(t *testing.T) {
	lim := DefaultLimits()
	if _, err := ReadCommand(br("*2\r\n$3\r\nSET\r\n+foo\r\n"), lim); err == nil {
		t.Fatal("expected error for non-bulk element")
	}
}

func TestReadCommandInvalidBulkLen(t *testing.T) {
	lim := DefaultLimits()
	if _, err := ReadCommand(br("*1\r\n$abc\r\n"), lim); err == nil {
		t.Fatal("expected error for invalid bulk len")
	}
}

func TestReadCommandTruncated(t *testing.T) {
	lim := DefaultLimits()
	if _, err := ReadCommand(br("*2\r\n$3\r\nSE"), lim); err == nil {
		t.Fatal("expected error for truncated input")
	}
}

func TestReadCommandTrailingGarbage(t *testing.T) {
	lim := DefaultLimits()
	// A bulk string must be followed by CRLF; here the 2 trailer bytes are XX.
	if _, err := ReadCommand(br("*2\r\n$3\r\nSET\r\n$1\r\nkXX"), lim); err == nil {
		t.Fatal("expected error for malformed bulk trailer")
	}
}

func TestReadCommandHeaderTooLong(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxHeaderLen = 4
	if _, err := ReadCommand(br("*99999\r\n"), lim); err == nil {
		t.Fatal("expected error for header too long")
	}
}

func TestReadCommandInlineTooLong(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxInlineLen = 3 // "PING" (4 bytes) exceeds this
	if _, err := ReadCommand(br("PING\r\n"), lim); err == nil {
		t.Fatal("expected error for inline too long")
	}
}

func TestReadCommandEmptyInline(t *testing.T) {
	lim := DefaultLimits()
	if _, err := ReadCommand(br("\r\n"), lim); err == nil {
		t.Fatal("expected error for empty inline")
	}
}

func TestWriterReplies(t *testing.T) {
	tests := []struct {
		name  string
		write func(*Writer) error
		want  string
	}{
		{"simple string", func(w *Writer) error { return w.SimpleString("OK") }, "+OK\r\n"},
		{"error", func(w *Writer) error { return w.Error("ERR something") }, "-ERR something\r\n"},
		{"integer", func(w *Writer) error { return w.Integer(42) }, ":42\r\n"},
		{"negative integer", func(w *Writer) error { return w.Integer(-1) }, ":-1\r\n"},
		{"bulk", func(w *Writer) error { return w.Bulk([]byte("hello")) }, "$5\r\nhello\r\n"},
		{"null bulk", func(w *Writer) error { return w.NullBulk() }, "$-1\r\n"},
		{"array len", func(w *Writer) error { return w.ArrayLen(2) }, "*2\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			if err := tt.write(w); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReplyWriteTo(t *testing.T) {
	tests := []struct {
		name  string
		reply Reply
		want  string
	}{
		{"simple", SimpleReply("OK"), "+OK\r\n"},
		{"error", ErrorReply("ERR bad"), "-ERR bad\r\n"},
		{"integer", IntegerReply(99), ":99\r\n"},
		{"bulk", BulkReply([]byte("v")), "$1\r\nv\r\n"},
		{"null", NullReply(), "$-1\r\n"},
		{"empty array", ArrayReply(nil), "*0\r\n"},
		{"nested array", ArrayReply([]Reply{SimpleReply("a"), IntegerReply(1)}), "*2\r\n+a\r\n:1\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			if err := tt.reply.WriteTo(w); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if got := buf.String(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadReplyRoundTrip(t *testing.T) {
	lim := DefaultLimits()
	replies := []Reply{
		SimpleReply("OK"),
		ErrorReply("ERR bad"),
		IntegerReply(42),
		BulkReply([]byte("hello")),
		NullReply(),
		ArrayReply([]Reply{IntegerReply(1), BulkReply([]byte("a")), NullReply(), SimpleReply("OK")}),
		ArrayReply(nil),
	}
	for _, want := range replies {
		var buf bytes.Buffer
		w := NewWriter(&buf)
		if err := want.WriteTo(w); err != nil {
			t.Fatalf("WriteTo: %v", err)
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
		got, err := ReadReply(bufio.NewReader(&buf), lim)
		if err != nil {
			t.Fatalf("ReadReply(%q): %v", buf.String(), err)
		}
		if got.Kind != want.Kind {
			t.Errorf("kind = %v, want %v (wire: %q)", got.Kind, want.Kind, buf.String())
		}
		switch want.Kind {
		case Simple, RError, Bulk:
			if string(got.Str) != string(want.Str) {
				t.Errorf("Str = %q, want %q", got.Str, want.Str)
			}
		case RInteger:
			if got.Int != want.Int {
				t.Errorf("Int = %d, want %d", got.Int, want.Int)
			}
		case RArray:
			if len(got.Array) != len(want.Array) {
				t.Errorf("array len = %d, want %d", len(got.Array), len(want.Array))
			}
		}
	}
}

func TestReadReplyNullArrayAsBulk(t *testing.T) {
	// RESP2 has no null array; a *-1 should decode to something non-error.
	lim := DefaultLimits()
	got, err := ReadReply(bufio.NewReader(strings.NewReader("*-1\r\n")), lim)
	if err != nil {
		t.Fatalf("ReadReply(*-1): %v", err)
	}
	if got.Kind != NullBulk {
		t.Errorf("kind = %v, want NullBulk", got.Kind)
	}
}

func TestReadReplyErrors(t *testing.T) {
	lim := DefaultLimits()
	cases := []string{
		"!",                     // unknown type
		"$abc\r\n",              // bad bulk len
		"$5\r\nhi\r\n",          // truncated bulk
		"$3\r\nhell\r\n",        // bad terminator
		":abc\r\n",              // bad integer
		"$9999999999999999\r\n", // len overflow
		"*\r\n",                 // bad array len
	}
	for _, wire := range cases {
		if _, err := ReadReply(bufio.NewReader(strings.NewReader(wire)), lim); err == nil {
			t.Errorf("ReadReply(%q): expected error, got nil", wire)
		}
	}
}
