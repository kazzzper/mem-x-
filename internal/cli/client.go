// Package cli implements the memx-cli RESP client: a small, stdlib-only
// client that sends commands to a mem-x (or Redis) server and decodes the
// replies. It reuses the internal/resp codec for both directions (Writer for
// commands, ReadReply for replies) so the wire behavior matches the server
// exactly. There is no third-party dependency (AGENTS.md §5 — runtime core
// stays stdlib-only).
package cli

import (
	"bufio"
	"net"
	"time"

	"mem-x/internal/resp"
)

// Client is a single-connection RESP client.
type Client struct {
	conn net.Conn
	br   *bufio.Reader
	w    *resp.Writer
	lim  resp.Limits
}

// Dial connects to addr with a connect timeout.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	return &Client{
		conn: conn,
		br:   bufio.NewReader(conn),
		w:    resp.NewWriter(conn),
		lim:  resp.DefaultLimits(),
	}, nil
}

// Do sends one command (name first, then args) and returns the decoded reply
// plus the round-trip latency. The returned latency covers write + read, so
// it is the per-request cost the CLI reports in ms.
func (c *Client) Do(args ...[]byte) (resp.Reply, time.Duration, error) {
	start := time.Now()
	if err := c.w.ArrayLen(len(args)); err != nil {
		return resp.Reply{}, 0, err
	}
	for _, a := range args {
		if err := c.w.Bulk(a); err != nil {
			return resp.Reply{}, 0, err
		}
	}
	if err := c.w.Flush(); err != nil {
		return resp.Reply{}, 0, err
	}
	reply, err := resp.ReadReply(c.br, c.lim)
	return reply, time.Since(start), err
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }
