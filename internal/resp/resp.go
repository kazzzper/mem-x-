// Package resp implements the wire protocol for mem-x: the Redis Serialization
// Protocol (RESP). Server replies are produced with Writer; client commands are
// read with ReadCommand, which accepts both the RESP multibulk form
// (*N\r\n$len\r\n...) and the inline form (one whitespace-separated line),
// matching Redis. Every read is bounded by Limits so a hostile client cannot
// force unbounded allocation (AGENTS.md §2.6).
package resp

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
)

// Limits caps protocol elements read from the network. All values are hard
// ceilings; input exceeding a cap is rejected with a ProtocolError instead of
// allocating unbounded memory.
type Limits struct {
	MaxBulkLen   int // max bytes in a bulk string
	MaxArgs      int // max elements in a command array (including the name)
	MaxInlineLen int // max length of an inline command line
	MaxHeaderLen int // max length of a *N / $N header line (Redis uses 64)
}

// DefaultLimits returns conservative limits for a small server.
func DefaultLimits() Limits {
	return Limits{
		MaxBulkLen:   64 << 20, // 64 MiB
		MaxArgs:      1024 * 1024,
		MaxInlineLen: 64 << 10, // 64 KiB (Redis inline-proto-max-len)
		MaxHeaderLen: 64,
	}
}

// ProtocolError marks input that violates the protocol or a limit. The server
// reports its message to the client after "ERR Protocol error: " and closes
// the connection.
type ProtocolError struct{ Msg string }

func (e *ProtocolError) Error() string { return e.Msg }

func perr(msg string) error { return &ProtocolError{Msg: msg} }

// ReadCommand reads one client command from br. The returned slice holds the
// command name followed by its arguments as raw bytes. ReadCommand accepts the
// multibulk RESP form (starting with '*') and the inline form (any other
// first byte).
func ReadCommand(br *bufio.Reader, lim Limits) ([][]byte, error) {
	b, err := br.Peek(1)
	if err != nil {
		return nil, err
	}
	if b[0] == '*' {
		return readMultibulk(br, lim)
	}
	return readInline(br, lim)
}

// readLine reads a \n- or \r\n-terminated line and returns it without the
// terminator. Lines longer than max are rejected. The what string names the
// element in error messages. Reads are bounded by the bufio buffer, so a
// hostile line cannot cause unbounded memory growth.
func readLine(br *bufio.Reader, max int, what string) ([]byte, error) {
	var line []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(frag) > 0 {
			line = append(line, frag...)
		}
		if err == bufio.ErrBufferFull {
			if len(line) > max {
				return nil, perr("too big " + what)
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		line = line[:len(line)-1] // strip '\n'
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if len(line) > max {
			return nil, perr("too big " + what)
		}
		return line, nil
	}
}

func readMultibulk(br *bufio.Reader, lim Limits) ([][]byte, error) {
	line, err := readLine(br, lim.MaxHeaderLen, "header line")
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(string(line[1:]))
	if err != nil {
		return nil, perr("invalid multibulk length")
	}
	if n <= 0 || n > lim.MaxArgs {
		return nil, perr("invalid multibulk length")
	}
	args := make([][]byte, n)
	for i := 0; i < n; i++ {
		bl, err := readLine(br, lim.MaxHeaderLen, "header line")
		if err != nil {
			return nil, err
		}
		if len(bl) == 0 || bl[0] != '$' {
			c := "?"
			if len(bl) > 0 {
				c = string(bl[0])
			}
			return nil, perr("expected '$', got '" + c + "'")
		}
		blen, err := strconv.Atoi(string(bl[1:]))
		if err != nil || blen < 0 || blen > lim.MaxBulkLen {
			return nil, perr("invalid bulk length")
		}
		buf := make([]byte, blen+2)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, err
		}
		if buf[blen] != '\r' || buf[blen+1] != '\n' {
			return nil, perr("invalid bulk length")
		}
		args[i] = buf[:blen]
	}
	return args, nil
}

func readInline(br *bufio.Reader, lim Limits) ([][]byte, error) {
	line, err := readLine(br, lim.MaxInlineLen, "inline request")
	if err != nil {
		return nil, err
	}
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return nil, perr("empty command")
	}
	if len(fields) > lim.MaxArgs {
		return nil, perr("too many arguments")
	}
	return fields, nil
}

// Writer serializes RESP replies. It buffers writes; callers must Flush.
type Writer struct {
	bw *bufio.Writer
}

// NewWriter returns a Writer over w.
func NewWriter(w io.Writer) *Writer { return &Writer{bw: bufio.NewWriter(w)} }

// Flush writes any buffered bytes to the underlying writer.
func (w *Writer) Flush() error { return w.bw.Flush() }

// SimpleString writes +s\r\n.
func (w *Writer) SimpleString(s string) error { return w.writeLine('+', []byte(s)) }

// Error writes -s\r\n.
func (w *Writer) Error(s string) error { return w.writeLine('-', []byte(s)) }

// Integer writes :n\r\n.
func (w *Writer) Integer(n int64) error {
	if err := w.bw.WriteByte(':'); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(strconv.FormatInt(n, 10)); err != nil {
		return err
	}
	_, err := w.bw.WriteString("\r\n")
	return err
}

// Bulk writes $len\r\n<data>\r\n.
func (w *Writer) Bulk(b []byte) error {
	if err := w.bw.WriteByte('$'); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(strconv.Itoa(len(b))); err != nil {
		return err
	}
	if _, err := w.bw.WriteString("\r\n"); err != nil {
		return err
	}
	if _, err := w.bw.Write(b); err != nil {
		return err
	}
	_, err := w.bw.WriteString("\r\n")
	return err
}

// NullBulk writes $-1\r\n.
func (w *Writer) NullBulk() error {
	_, err := w.bw.WriteString("$-1\r\n")
	return err
}

// ArrayLen writes *N\r\n.
func (w *Writer) ArrayLen(n int) error {
	if err := w.bw.WriteByte('*'); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(strconv.Itoa(n)); err != nil {
		return err
	}
	_, err := w.bw.WriteString("\r\n")
	return err
}

func (w *Writer) writeLine(prefix byte, payload []byte) error {
	if err := w.bw.WriteByte(prefix); err != nil {
		return err
	}
	if _, err := w.bw.Write(payload); err != nil {
		return err
	}
	_, err := w.bw.WriteString("\r\n")
	return err
}

// ReplyKind enumerates the RESP reply types the server produces.
type ReplyKind uint8

const (
	Simple   ReplyKind = iota // +...
	RError                    // -...
	RInteger                  // :...
	Bulk                      // $len\r\n...
	NullBulk                  // $-1\r\n
	RArray                    // *N\r\n followed by N replies
)

// Reply is a server reply to a client command: a small tagged union in which
// only the fields matching Kind are meaningful.
type Reply struct {
	Kind  ReplyKind
	Str   []byte
	Int   int64
	Array []Reply
}

func SimpleReply(s string) Reply { return Reply{Kind: Simple, Str: []byte(s)} }
func ErrorReply(s string) Reply  { return Reply{Kind: RError, Str: []byte(s)} }
func IntegerReply(n int64) Reply { return Reply{Kind: RInteger, Int: n} }
func BulkReply(b []byte) Reply   { return Reply{Kind: Bulk, Str: b} }
func NullReply() Reply           { return Reply{Kind: NullBulk} }
func ArrayReply(a []Reply) Reply { return Reply{Kind: RArray, Array: a} }

// WriteTo serializes r to w.
func (r Reply) WriteTo(w *Writer) error {
	switch r.Kind {
	case Simple:
		return w.SimpleString(string(r.Str))
	case RError:
		return w.Error(string(r.Str))
	case RInteger:
		return w.Integer(r.Int)
	case Bulk:
		return w.Bulk(r.Str)
	case NullBulk:
		return w.NullBulk()
	case RArray:
		if err := w.ArrayLen(len(r.Array)); err != nil {
			return err
		}
		for _, el := range r.Array {
			if err := el.WriteTo(w); err != nil {
				return err
			}
		}
		return nil
	}
	return perr("unknown reply kind")
}
