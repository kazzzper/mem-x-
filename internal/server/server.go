// Package server hosts mem-x over TCP: an accept loop, one goroutine per
// connection, per-connection protocol handling, and graceful shutdown. Go's
// netpoller provides epoll/kqueue under the hood, so no manual event loop is
// needed (PLAN.md §3).
package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
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

	ln    net.Listener
	sem   chan struct{} // connection slots
	conns sync.Map      // net.Conn -> struct{}
	wg    sync.WaitGroup
}

// New returns a Server wired to the given store, registry, and stats.
func New(cfg config.Config, st *store.Store, reg *command.Registry, stats *command.Stats) *Server {
	if cfg.MaxConn < 1 {
		cfg.MaxConn = 1 // a 0/negative slot count would reject every connection
	}
	return &Server{
		cfg:   cfg,
		store: st,
		reg:   reg,
		stats: stats,
		sem:   make(chan struct{}, cfg.MaxConn),
	}
}

// Listen binds the server to addr and returns the bound address (useful when
// addr contains port 0).
func (s *Server) Listen(addr string) (net.Addr, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
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

	br := bufio.NewReaderSize(conn, 16<<10)
	bw := resp.NewWriter(bufio.NewWriterSize(conn, 16<<10))
	lim := resp.Limits{
		MaxBulkLen:   s.cfg.MaxBulkLen,
		MaxArgs:      s.cfg.MaxArgs,
		MaxInlineLen: s.cfg.MaxInlineLen,
		MaxHeaderLen: 64,
	}

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
