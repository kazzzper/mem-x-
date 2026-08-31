package memxurl_test

import (
	"strings"
	"testing"

	"mem-x/internal/memxurl"
)

func TestParse(t *testing.T) {
	tests := []struct {
		raw      string
		scheme   string
		host     string
		port     string
		user     string
		password string
		db       int
		tls      bool
	}{
		{"memx://localhost:6379", "memx", "localhost", "6379", "", "", 0, false},
		{"memx://127.0.0.1", "memx", "127.0.0.1", "6379", "", "", 0, false},
		{"memx://host.example.com:7000", "memx", "host.example.com", "7000", "", "", 0, false},
		{"memx://alice:secret@localhost:6379/2", "memx", "localhost", "6379", "alice", "secret", 2, false},
		{"memx://:onlypass@localhost", "memx", "localhost", "6379", "", "onlypass", 0, false},
		{"memx://localhost/3", "memx", "localhost", "6379", "", "", 3, false},
		{"memxs://localhost", "memxs", "localhost", "6380", "", "", 0, true},
		{"memxs://bob@secure.example.com:9999/5", "memxs", "secure.example.com", "9999", "bob", "", 5, true},
		{"memx://[::1]:6379", "memx", "::1", "6379", "", "", 0, false},
	}
	for _, tt := range tests {
		u, err := memxurl.Parse(tt.raw)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tt.raw, err)
			continue
		}
		if u.Scheme != tt.scheme {
			t.Errorf("Parse(%q) scheme = %q, want %q", tt.raw, u.Scheme, tt.scheme)
		}
		if u.Host != tt.host {
			t.Errorf("Parse(%q) host = %q, want %q", tt.raw, u.Host, tt.host)
		}
		if u.Port != tt.port {
			t.Errorf("Parse(%q) port = %q, want %q", tt.raw, u.Port, tt.port)
		}
		if u.User != tt.user {
			t.Errorf("Parse(%q) user = %q, want %q", tt.raw, u.User, tt.user)
		}
		if u.Password != tt.password {
			t.Errorf("Parse(%q) password = %q, want %q", tt.raw, u.Password, tt.password)
		}
		if u.DB != tt.db {
			t.Errorf("Parse(%q) db = %d, want %d", tt.raw, u.DB, tt.db)
		}
		if u.TLS != tt.tls {
			t.Errorf("Parse(%q) TLS = %v, want %v", tt.raw, u.TLS, tt.tls)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, raw := range []string{
		"redis://localhost:6379",
		"http://example.com",
		"memx://",
		"memx://localhost/notanumber",
		"://bad",
	} {
		if _, err := memxurl.Parse(raw); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", raw)
		}
	}
}

func TestAddr(t *testing.T) {
	u, err := memxurl.Parse("memx://localhost")
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Addr(); got != "localhost:6379" {
		t.Errorf("Addr() = %q, want localhost:6379", got)
	}

	u, err = memxurl.Parse("memxs://secure.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Addr(); got != "secure.example.com:6380" {
		t.Errorf("Addr() = %q, want secure.example.com:6380", got)
	}
}

func TestString(t *testing.T) {
	u, err := memxurl.Parse("memx://alice:secret@localhost:6379/2")
	if err != nil {
		t.Fatal(err)
	}
	s := u.String()
	if !strings.HasPrefix(s, "memx://alice:****@localhost") {
		t.Errorf("String() = %q, want password masked", s)
	}
	if strings.Contains(s, "secret") {
		t.Errorf("String() must not leak the password: %q", s)
	}
}

func TestIsLocalAddr(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"memx://localhost:6379", true},
		{"memx://127.0.0.1:6379", true},
		{"memx://[::1]:6379", true},
		{"memx://0.0.0.0:6379", true},
		{"memx://remote.example.com:6379", false},
		{"memx://192.168.1.10:6379", false},
	}
	for _, tt := range tests {
		u, err := memxurl.Parse(tt.raw)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", tt.raw, err)
		}
		if got := u.IsLocalAddr(); got != tt.want {
			t.Errorf("IsLocalAddr(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}
