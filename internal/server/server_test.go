package server_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mem-x/internal/command"
	"mem-x/internal/config"
	"mem-x/internal/persist"
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

// startServerWithAOF boots a server wired with an AOF log at path (mirroring
// cmd/mem-x main), so its writes are persisted. Returns the bound address.
func startServerWithAOF(t *testing.T, cfg config.Config, path string) string {
	t.Helper()
	st := store.New(store.WithShards(4))
	stats := command.NewStats()
	reg := command.New(st, stats)

	aof, err := persist.Open(path, persist.FsyncAlways)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persist.Load(path, reg); err != nil {
		t.Fatal(err)
	}
	reg.SetPropagator(func(args [][]byte) {
		if err := aof.Append(args); err != nil {
			t.Errorf("AOF append failed: %v", err)
		}
	})
	t.Cleanup(func() { _ = aof.Close() })

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

// TestAOFServerLifecycle writes through the server with an AOF attached, then
// proves a fresh server loading the same file rebuilds the exact state.
func TestAOFServerLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.aof")

	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"

	// --- First server: write some data over the wire. ---
	addr := startServerWithAOF(t, cfg, path)
	conn := dial(t, addr)
	r := bufio.NewReader(conn)

	send(t, conn, "*3\r\n$3\r\nSET\r\n$2\r\nk1\r\n$2\r\nv1\r\n")
	if got := readLine(t, r); got != "+OK" {
		t.Fatalf("SET got %q", got)
	}
	send(t, conn, "*3\r\n$3\r\nSET\r\n$2\r\nk2\r\n$4\r\n1000\r\n")
	if got := readLine(t, r); got != "+OK" {
		t.Fatalf("SET got %q", got)
	}
	send(t, conn, "*3\r\n$6\r\nINCRBY\r\n$2\r\nk2\r\n$2\r\n23\r\n")
	if got := readLine(t, r); got != ":1023" {
		t.Fatalf("INCRBY got %q", got)
	}
	// Prove the AOF file exists and is non-empty.
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("expected a non-empty AOF file, stat err=%v size=%d", err, info.Size())
	}

	// --- Second server: fresh store, load the same AOF. ---
	st2 := store.New(store.WithShards(4))
	reg2 := command.New(st2, command.NewStats())
	n, err := persist.Load(path, reg2)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected commands replayed from AOF")
	}
	if v, ok := st2.Get([]byte("k1")); !ok || string(v) != "v1" {
		t.Fatalf("k1 = %q ok=%v, want v1", string(v), ok)
	}
	if v, ok := st2.Get([]byte("k2")); !ok || string(v) != "1023" {
		t.Fatalf("k2 = %q ok=%v, want 1023", string(v), ok)
	}
}

// writeSelfSignedCert writes a PEM cert + key valid for 127.0.0.1/localhost
// into t's temp dir and returns both paths.
func writeSelfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestServerTLS(t *testing.T) {
	certFile, keyFile := writeSelfSignedCert(t)

	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.TLSCertFile = certFile
	cfg.TLSKeyFile = keyFile

	st := store.New(store.WithShards(4))
	stats := command.NewStats()
	reg := command.New(st, stats)
	srv := server.New(cfg, st, reg, stats)
	if !srv.TLSEnabled() {
		t.Fatal("TLSEnabled() = false, want true")
	}
	addr, err := srv.Listen(cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx)

	// A TLS client with skip-verify (self-signed) must work end to end.
	tlsCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
	conn, err := tls.Dial("tcp", addr.String(), tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	r := bufio.NewReader(conn)
	send(t, conn, "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n")
	if got := readLine(t, r); got != "+OK" {
		t.Fatalf("SET got %q", got)
	}
	send(t, conn, "*2\r\n$3\r\nGET\r\n$1\r\nk\r\n")
	if got := readBulk(t, r); got != "v" {
		t.Fatalf("GET got %q, want v", got)
	}

	// A plaintext dial must fail the TLS handshake (server speaks TLS only).
	plain, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	plain.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = plain.Read(buf)
	plain.Close()
	if err == nil {
		t.Fatal("expected plaintext read to fail on a TLS-only listener")
	}
}

func TestServerTLSPartialConfigFallsBackToPlaintext(t *testing.T) {
	// Only a cert, no key: the server must still start, plaintext, and log.
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.TLSCertFile = "/nonexistent/cert.pem"

	st := store.New(store.WithShards(4))
	srv := server.New(cfg, st, command.New(st, command.NewStats()), command.NewStats())
	if srv.TLSEnabled() {
		t.Fatal("TLSEnabled() = true with a partial TLS config, want false")
	}
	addr, err := srv.Listen(cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx)

	conn := dial(t, addr.String())
	defer conn.Close()
	r := bufio.NewReader(conn)
	send(t, conn, "*1\r\n$4\r\nPING\r\n")
	if got := readLine(t, r); got != "+PONG" {
		t.Fatalf("PING got %q", got)
	}
}
