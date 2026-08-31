package cli

import (
	"context"
	"net"
	"strings"

	"mem-x/internal/command"
	"mem-x/internal/config"
	"mem-x/internal/server"
	"mem-x/internal/store"
)

// Embedded is an in-process mem-x server listening on a TCP address. The
// caller starts it with StartEmbedded and must call Close() to shut it down.
// The server lives and dies with the caller (session-scoped).
type Embedded struct {
	addr   string
	store  *store.Store
	server *server.Server
	cancel context.CancelFunc
	done   chan struct{}
}

// StartEmbedded boots an in-process mem-x server on addr and returns a handle
// that reports the bound address and can be used to shut it down. The server
// is fully functional — the caller can connect to it over TCP via the normal
// Client.Dial path.
func StartEmbedded(addr string) (*Embedded, error) {
	cfg := config.Default()
	cfg.Addr = addr
	// Use the same default shard count as the standalone server.
	st := store.New(store.WithShards(cfg.Shards))
	stats := command.NewStats()
	reg := command.New(st, stats)
	srv := server.New(cfg, st, reg, stats)

	bound, err := srv.Listen(addr)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	st.StartExpiry(ctx, cfg.TTLTick)

	go func() {
		defer close(done)
		_ = srv.Serve(ctx)
	}()

	return &Embedded{
		addr:   bound.String(),
		store:  st,
		server: srv,
		cancel: cancel,
		done:   done,
	}, nil
}

// Addr returns the bound address (useful when the port was 0 or auto-assigned
// by the embedded server's listenWithRetry-reassign path).
func (e *Embedded) Addr() string { return e.addr }

// Close shuts the embedded server down gracefully: it cancels the server
// context (which closes the listener, drains connections) and waits for the
// serve goroutine to return.
func (e *Embedded) Close() {
	e.cancel()
	<-e.done
}

// IsLocalAddr reports whether addr targets a loopback or unspecified (all
// interfaces) address, which is the only case auto-spawn is safe—we can't
// bind a remote host's address locally.
func IsLocalAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// SplitHostPort wants bracketed IPv6 ([::1]:6379); also accept the
		// common unbracketed form (::1:6379) by splitting at the last colon.
		if i := strings.LastIndex(addr, ":"); i > 0 {
			host = addr[:i]
		} else {
			host = addr
		}
	}
	if host == "" {
		return true // empty host = localhost (":6379")
	}
	host = strings.ToLower(host)
	if host == "localhost" || host == "localhost6" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false // a non-IP hostname (e.g. "redis.example.com") is not local
	}
	return ip.IsLoopback() || ip.IsUnspecified()
}
