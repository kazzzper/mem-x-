package cli

import (
	"bufio"
	"net"
	"testing"
	"time"

	"mem-x/internal/resp"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"PING", []string{"PING"}},
		{"SET k v", []string{"SET", "k", "v"}},
		{"  SET   k   v  ", []string{"SET", "k", "v"}},
		{"GET \"my key\"", []string{"GET", "my key"}},
		{`SET k "a b c"`, []string{"SET", "k", "a b c"}},
		{`ECHO "say \"hi\""`, []string{"ECHO", `say "hi"`}},
		{"", nil},
		{"   ", nil},
		{"SCAN 0 MATCH user:* COUNT 10", []string{"SCAN", "0", "MATCH", "user:*", "COUNT", "10"}},
		{"KEYS user*", []string{"KEYS", "user*"}},
	}
	for _, tc := range tests {
		got := Tokenize(tc.line)
		if len(got) != len(tc.want) {
			t.Errorf("Tokenize(%q) = %v, want %v", tc.line, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Tokenize(%q) = %v, want %v", tc.line, got, tc.want)
				break
			}
		}
	}
}

func TestFormatReply(t *testing.T) {
	tests := []struct {
		name string
		rep  resp.Reply
		want string
	}{
		{"simple", resp.SimpleReply("OK"), "OK"},
		{"error", resp.ErrorReply("ERR bad"), "(error) ERR bad"},
		{"integer", resp.IntegerReply(42), "(integer) 42"},
		{"bulk", resp.BulkReply([]byte("hi")), `"hi"`},
		{"null", resp.NullReply(), "(nil)"},
		{"empty array", resp.ArrayReply(nil), "(empty array)"},
		{"array", resp.ArrayReply([]resp.Reply{
			resp.SimpleReply("a"),
			resp.IntegerReply(1),
			resp.NullReply(),
		}), "1) a\n2) (integer) 1\n3) (nil)"},
	}
	for _, tc := range tests {
		if got := FormatReply(tc.rep); got != tc.want {
			t.Errorf("%s: FormatReply = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFormatLatency(t *testing.T) {
	got := FormatLatency(420 * time.Microsecond)
	if got != "(0.42 ms)" {
		t.Errorf("FormatLatency(420µs) = %q, want (0.42 ms)", got)
	}
}

// fakeServer accepts one connection, reads one multibulk command, and replies
// based on the first token. Used to test Client.Do over a real socket.
func fakeServer(t *testing.T, cmd string, reply resp.Reply) (addr string, done chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done = make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if line != "*3\r\n" { // SET k v → 3 elements
			return
		}
		// Skip the three bulk strings.
		for i := 0; i < 3; i++ {
			bl, err := br.ReadString('\n')
			if err != nil {
				return
			}
			_ = bl
			// read the payload + CRLF (bulk body)
			buf := make([]byte, 3)
			if _, err := br.Read(buf); err != nil {
				return
			}
		}
		w := resp.NewWriter(conn)
		if err := reply.WriteTo(w); err != nil {
			return
		}
		_ = w.Flush()
	}()
	return ln.Addr().String(), done
}

func TestClientDo(t *testing.T) {
	addr, done := fakeServer(t, "SET", resp.SimpleReply("OK"))
	c, err := Dial(addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	reply, d, err := c.Do([]byte("SET"), []byte("k"), []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	if reply.Kind != resp.Simple || string(reply.Str) != "OK" {
		t.Fatalf("reply = %v, want Simple OK", reply)
	}
	if d <= 0 {
		t.Fatalf("latency = %v, want > 0", d)
	}
	<-done
}
