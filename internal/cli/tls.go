package cli

import (
	"bufio"
	"crypto/tls"
	"net"
	"time"

	"mem-x/internal/resp"
)

// DialTLS is like Dial but connects over TLS. When skipVerify is true the
// server's certificate chain and hostname are not verified (equivalent to
// redis-cli's --tls-skip-verify flag).
func DialTLS(addr string, timeout time.Duration, skipVerify bool) (*Client, error) {
	d := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		InsecureSkipVerify: skipVerify, // #nosec G402 — user-requested via flag
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		conn: conn,
		br:   bufio.NewReader(conn),
		w:    resp.NewWriter(conn),
		lim:  resp.DefaultLimits(),
	}, nil
}
