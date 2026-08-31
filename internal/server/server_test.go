package server_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"mem-x/internal/command"
	"mem-x/internal/config"
	"mem-x/internal/server"
	"mem-x/internal/store"
)

// startServer boots a server on an ephemeral port and returns its address.
func startServer(t *testing.T, cfg config.Config) string {
	t.Helper()
	st := store.New(store.WithShards(4))
	stats := command.NewStats()
	reg := command.New(st, stats)
	srv := server.New(cfg, st, reg, stats)
	addr, err := srv.Listen(cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx)
	return addr.String()
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func send(t *testing.T, conn net.Conn, s string) {
	t.Helper()
	if _, err := conn.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

// readLine reads one CRLF-terminated line.
func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(line, "\r\n")
}

// readBulk reads a $len\r\n<data>\r\n reply and returns the payload.
func readBulk(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	hdr := readLine(t, r)
	if hdr == "$-1" {
		return ""
	}
	var n int
	if _, err := fmt.Sscanf(hdr, "$%d", &n); err != nil {
		t.Fatalf("bad bulk header %q: %v", hdr, err)
	}
	buf := make([]byte, n+2)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

func TestPingInline(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	addr := startServer(t, cfg)

	conn := dial(t, addr)
	send(t, conn, "PING\r\n")
	r := bufio.NewReader(conn)
	if got := readLine(t, r); got != "+PONG" {
		t.Fatalf("got %q", got)
	}
}

func TestSetGetRESP(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	addr := startServer(t, cfg)

	conn := dial(t, addr)
	r := bufio.NewReader(conn)

	send(t, conn, "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n")
	if got := readLine(t, r); got != "+OK" {
		t.Fatalf("got %q", got)
	}
	send(t, conn, "*2\r\n$3\r\nGET\r\n$1\r\nk\r\n")
	if got := readBulk(t, r); got != "v" {
		t.Fatalf("got %q", got)
	}
	send(t, conn, "*2\r\n$3\r\nGET\r\n$7\r\nmissing\r\n")
	if got := readBulk(t, r); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestUnknownCommandOverWire(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	addr := startServer(t, cfg)

	conn := dial(t, addr)
	send(t, conn, "BOGUS a\r\n")
	r := bufio.NewReader(conn)
	got := readLine(t, r)
	if !strings.HasPrefix(got, "-ERR unknown command 'bogus'") {
		t.Fatalf("got %q", got)
	}
}

func TestBulkLimitRejected(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.MaxBulkLen = 16
	addr := startServer(t, cfg)

	conn := dial(t, addr)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	big := strings.Repeat("x", 100)
	send(t, conn, fmt.Sprintf("*2\r\n$3\r\nGET\r\n$100\r\n%s\r\n", big))
	r := bufio.NewReader(conn)
	if got := readLine(t, r); got != "-ERR Protocol error: invalid bulk length" {
		t.Fatalf("got %q", got)
	}
	// The connection must be closed after a protocol error.
	if _, err := r.ReadByte(); err == nil {
		t.Fatal("expected closed connection after protocol error")
	}
}

func TestMaxConn(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.MaxConn = 1
	addr := startServer(t, cfg)

	first := dial(t, addr)
	send(t, first, "PING\r\n")
	r1 := bufio.NewReader(first)
	if got := readLine(t, r1); got != "+PONG" {
		t.Fatalf("got %q", got)
	}

	second, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	send(t, second, "PING\r\n")
	r2 := bufio.NewReader(second)
	if got := readLine(t, r2); got != "-ERR max number of clients reached" {
		t.Fatalf("got %q", got)
	}

	// The first connection keeps working.
	send(t, first, "PING\r\n")
	if got := readLine(t, r1); got != "+PONG" {
		t.Fatalf("got %q", got)
	}
}

func TestIdleTimeout(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.IdleTimeout = 200 * time.Millisecond
	addr := startServer(t, cfg)

	conn := dial(t, addr)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected connection closed after idle timeout")
	}
}

func TestConcurrentClients(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	addr := startServer(t, cfg)

	const clients = 16
	const perClient = 100
	var wg sync.WaitGroup
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Errorf("dial: %v", err)
				return
			}
			defer conn.Close()
			r := bufio.NewReader(conn)
			key := fmt.Sprintf("k%d", c)
			write := func(s string) {
				if _, err := conn.Write([]byte(s)); err != nil {
					t.Errorf("write: %v", err)
				}
			}
			write(fmt.Sprintf("SET %s v\r\n", key))
			line, err := r.ReadString('\n')
			if err != nil || strings.TrimRight(line, "\r\n") != "+OK" {
				t.Errorf("SET: line=%q err=%v", line, err)
				return
			}
			for i := 0; i < perClient; i++ {
				write(fmt.Sprintf("GET %s\r\n", key))
				hdr, err := r.ReadString('\n')
				if err != nil {
					t.Errorf("GET hdr: %v", err)
					return
				}
				var n int
				fmt.Sscanf(strings.TrimRight(hdr, "\r\n"), "$%d", &n)
				buf := make([]byte, n+2)
				if _, err := io.ReadFull(r, buf); err != nil || string(buf[:n]) != "v" {
					t.Errorf("GET body: %q err=%v", string(buf[:n]), err)
					return
				}
			}
		}(c)
	}
	wg.Wait()
}

func TestGracefulShutdown(t *testing.T) {
	st := store.New(store.WithShards(4))
	stats := command.NewStats()
	reg := command.New(st, stats)
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	srv := server.New(cfg, st, reg, stats)
	addr, err := srv.Listen(cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()

	conn := dial(t, addr.String())
	send(t, conn, "PING\r\n")
	r := bufio.NewReader(conn)
	if got := readLine(t, r); got != "+PONG" {
		t.Fatalf("got %q", got)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	// The server closes live connections during drain.
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := r.ReadByte(); err == nil {
		t.Fatal("expected closed connection after shutdown")
	}
}
