// Package server hosts mem-x over TCP: an accept loop, one goroutine per
// connection, per-connection protocol handling, and graceful shutdown. Go's
// netpoller provides epoll/kqueue under the hood, so no manual event loop is
// needed (PLAN.md §3).
package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"mem-x/internal/command"
	"mem-x/internal/config"
	"mem-x/internal/resp"
	"mem-x/internal/store"
)

// Server accepts and serves TCP client connections.
type Server struct {
	cfg   config.Config
	store *store.Store
	reg   *command.Registry
	stats *command.Stats

	ln        net.Listener
	tlsConfig *tls.Config   // non-nil when TLS is enabled
	sem       chan struct{} // connection slots
	conns     sync.Map      // net.Conn -> struct{}
	wg        sync.WaitGroup

	// readerPool/writerPool reuse the per-connection bufio buffers (16 KiB
	// each). A connection owns its bufio exclusively, so Reset-and-reuse is
	// safe; the command tokens/bulk buffers are NOT pooled because they escape
	// into the store as retained values (PLAN.md §3, AGENTS.md §2.7).
	readerPool sync.Pool // *bufio.Reader with a 16 KiB buffer
	writerPool sync.Pool // *resp.Writer with a 16 KiB buffer
}

// New returns a Server wired to the given store, registry, and stats. When
// cfg.TLSCertFile and cfg.TLSKeyFile are both set, the listener is wrapped in
// TLS (clients connect with memxs:// URLs).
func New(cfg config.Config, st *store.Store, reg *command.Registry, stats *command.Stats) *Server {
	if cfg.MaxConn < 1 {
		cfg.MaxConn = 1 // a 0/negative slot count would reject every connection
	}
	return &Server{
		cfg:       cfg,
		store:     st,
		reg:       reg,
		stats:     stats,
		sem:       make(chan struct{}, cfg.MaxConn),
		tlsConfig: loadTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile),
		readerPool: sync.Pool{New: func() any {
			return bufio.NewReaderSize(nil, 16<<10)
		}},
		writerPool: sync.Pool{New: func() any {
			return resp.NewWriter(nil)
		}},
	}
}

// loadTLSConfig builds a *tls.Config from the cert/key files. It returns nil
// when neither is set (plaintext). A partial pair (one set, the other empty)
// or an unreadable pair is logged and also yields nil — the server keeps
// serving plaintext rather than failing to start.
func loadTLSConfig(certFile, keyFile string) *tls.Config {
	if certFile == "" && keyFile == "" {
		return nil
	}
	if certFile == "" || keyFile == "" {
		slog.Warn("TLS requires both -tls-cert and -tls-key; falling back to plaintext",
			"cert", certFile, "key", keyFile)
		return nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		slog.Error("cannot load TLS key pair; falling back to plaintext", "err", err)
		return nil
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
}

// TLSEnabled reports whether the server was configured with a TLS listener.
func (s *Server) TLSEnabled() bool { return s.tlsConfig != nil }

// Listen binds the server to addr and returns the bound address (useful when
// addr contains port 0). When TLS is enabled the listener is wrapped so every
// accepted connection performs a TLS handshake.
func (s *Server) Listen(addr string) (net.Addr, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	if s.tlsConfig != nil {
		ln = tls.NewListener(ln, s.tlsConfig)
	}
	s.ln = ln
	return ln.Addr(), nil
}

// Serve runs the accept loop until ctx is cancelled, then shuts down
// gracefully: it stops accepting, closes all live connections to unblock
// their handlers, and waits for in-flight commands to finish.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return errors.New("server: not listening")
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.acceptLoop(ctx, s.ln) }()

	<-ctx.Done()
	_ = s.ln.Close()
	s.conns.Range(func(k, _ any) bool {
		_ = k.(net.Conn).Close()
		return true
	})
	s.wg.Wait()
	return <-errCh
}

func (s *Server) acceptLoop(ctx context.Context, ln net.Listener) error {
	var delay time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Transient accept error: back off and retry.
			if delay == 0 {
				delay = 5 * time.Millisecond
			} else {
				delay *= 2
				if delay > time.Second {
					delay = time.Second
				}
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		delay = 0

		select {
		case s.sem <- struct{}{}:
		default:
			// Connection cap reached: reject like Redis.
			_ = s.writeClientError(conn, "ERR max number of clients reached")
			_ = conn.Close()
			continue
		}
		s.stats.ConnectedClients.Add(1)
		s.conns.Store(conn, struct{}{})
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

// handleConn serves one client connection until error, idle timeout, or
// shutdown. recover() runs only at this boundary, so a handler bug can never
// take down the process (AGENTS.md §2.4).
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic in connection handler", "err", r)
		}
		s.conns.Delete(conn)
		<-s.sem
		s.stats.ConnectedClients.Add(-1)
		_ = conn.Close()
	}()

	br := s.readerPool.Get().(*bufio.Reader)
	br.Reset(conn)
	defer s.readerPool.Put(br)
	bw := s.writerPool.Get().(*resp.Writer)
	bw.Reset(conn)
	defer s.writerPool.Put(bw)
	lim := resp.Limits{
		MaxBulkLen:   s.cfg.MaxBulkLen,
		MaxArgs:      s.cfg.MaxArgs,
		MaxInlineLen: s.cfg.MaxInlineLen,
		MaxHeaderLen: 64,
	}
	authed := s.cfg.RequirePass == "" // no password → pre-authed

	for {
		if ctx.Err() != nil {
			return
		}
		if s.cfg.IdleTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		} else {
			_ = conn.SetReadDeadline(time.Time{})
		}
		tokens, err := resp.ReadCommand(br, lim)
		if err != nil {
			s.handleReadError(conn, bw, err)
			return
		}
		// Auth gate: when a password is required, only AUTH is accepted
		// before the connection is authenticated (Redis-compatible NOAUTH
		// semantics).
		if !authed {
			name := strings.ToLower(string(tokens[0]))
			if name != "auth" {
				_ = bw.Error("NOAUTH Authentication required.")
				_ = bw.Flush()
				continue
			}
			if len(tokens) != 2 {
				_ = bw.Error("ERR wrong number of arguments for 'auth' command")
				_ = bw.Flush()
				continue
			}
			if subtle.ConstantTimeCompare(tokens[1], []byte(s.cfg.RequirePass)) == 1 {
				authed = true
				_ = bw.SimpleString("OK")
				_ = bw.Flush()
			} else {
				_ = bw.Error("WRONGPASS invalid username-password pair or user is disabled.")
				_ = bw.Flush()
			}
			continue
		}
		reply := s.reg.Execute(ctx, tokens)
		if err := reply.WriteTo(bw); err != nil {
			return
		}
		if err := bw.Flush(); err != nil {
			return
		}
	}
}

// handleReadError classifies a read failure. Protocol errors are reported to
// the client before closing; timeouts, EOF, and unexpected errors close
// silently (the connection is torn down either way).
func (s *Server) handleReadError(conn net.Conn, bw *resp.Writer, err error) {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return
	}
	var pe *resp.ProtocolError
	if errors.As(err, &pe) {
		_ = bw.Error("ERR Protocol error: " + pe.Msg)
		_ = bw.Flush()
		return
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return // idle timeout: just close
	}
	// Any other read failure: close silently.
}

// writeClientError sends a one-shot error reply and flushes. Used for
// connection-level rejections before the handler goroutine exists.
func (s *Server) writeClientError(conn net.Conn, msg string) error {
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	w := resp.NewWriter(conn)
	if err := w.Error(msg); err != nil {
		return err
	}
	return w.Flush()
}
