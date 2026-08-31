// Package memxurl parses memx:// and memxs:// connection URLs, extracting
// host, port, credentials, and database index — the same way redis:// and
// rediss:// work for Redis.
//
// Format:
//
//	memx://[[user]:[password]@]host[:port][/db]
//	memxs://[[user]:[password]@]host[:port][/db]
//
// Default ports: 6379 for memx://, 6380 for memxs://.
package memxurl

import (
	"fmt"
	"net"
	neturl "net/url"
	"strconv"
	"strings"
)

// URL holds the parsed components of a memx:// or memxs:// URL.
type URL struct {
	Scheme   string // "memx" or "memxs"
	Host     string
	Port     string
	User     string
	Password string
	DB       int  // 0 if not specified
	TLS      bool // true for memxs://
}

// Parse parses a memx:// or memxs:// URL and returns its components.
// An error is returned for unsupported schemes or malformed URLs.
func Parse(rawURL string) (*URL, error) {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("memxurl: %w", err)
	}
	switch u.Scheme {
	case "memx", "memxs":
	default:
		return nil, fmt.Errorf("memxurl: unsupported scheme %q (want memx:// or memxs://)", u.Scheme)
	}

	result := &URL{
		Scheme: u.Scheme,
		TLS:    u.Scheme == "memxs",
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("memxurl: %q: no host specified", rawURL)
	}
	result.Host = host

	port := u.Port()
	if port == "" {
		port = DefaultPort(u.Scheme)
	}
	result.Port = port

	if u.User != nil {
		result.User = u.User.Username()
		result.Password, _ = u.User.Password()
	}

	// Parse database from path: /0, /1, etc.
	path := strings.TrimLeft(u.Path, "/")
	if path != "" {
		db, err := strconv.Atoi(path)
		if err != nil {
			return nil, fmt.Errorf("memxurl: invalid db %q", u.Path)
		}
		result.DB = db
	}

	return result, nil
}

// DefaultPort returns the default TCP port for the given scheme.
func DefaultPort(scheme string) string {
	switch scheme {
	case "memxs":
		return "6380"
	default:
		return "6379"
	}
}

// Addr returns the host:port string suitable for net.Dial.
func (u *URL) Addr() string {
	return net.JoinHostPort(u.Host, u.Port)
}

// String reconstructs the canonical URL string (without password for safety
// when the caller wants to display it).
func (u *URL) String() string {
	var b strings.Builder
	b.WriteString(u.Scheme)
	b.WriteString("://")
	if u.User != "" {
		b.WriteString(u.User)
		if u.Password != "" {
			b.WriteString(":****")
		}
		b.WriteString("@")
	}
	b.WriteString(u.Host)
	if u.Port != "" && u.Port != DefaultPort(u.Scheme) {
		b.WriteString(":")
		b.WriteString(u.Port)
	}
	if u.DB != 0 {
		b.WriteString("/")
		b.WriteString(strconv.Itoa(u.DB))
	}
	return b.String()
}

// IsLocalAddr reports whether the URL points to a local (loopback,
// unspecified, or localhost) address. Used by the CLI to decide whether
// auto-spawn is possible.
func (u *URL) IsLocalAddr() bool {
	host := u.Host
	// Strip IPv6 brackets for matching.
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "::1" || host == "localhost" || host == "0.0.0.0"
}