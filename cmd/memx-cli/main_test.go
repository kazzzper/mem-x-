package main

import "testing"

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		arg      string
		dialAddr string
		password string
		db       int
		tls      bool
		wantErr  bool
	}{
		{"127.0.0.1:6379", "127.0.0.1:6379", "", 0, false, false},
		{"localhost", "localhost", "", 0, false, false},
		{":7000", ":7000", "", 0, false, false},
		{"memx://localhost", "localhost:6379", "", 0, false, false},
		{"memx://localhost:7000", "localhost:7000", "", 0, false, false},
		{"memx://alice:secret@localhost:6379/2", "localhost:6379", "secret", 2, false, false},
		{"memxs://secure.example.com", "secure.example.com:6380", "", 0, true, false},
		{"memxs://bob@host:9999/5", "host:9999", "", 5, true, false},
		{"http://example.com", "", "", 0, false, true},
		{"memx://", "", "", 0, false, true},
	}
	for _, tt := range tests {
		got, err := resolveTarget(tt.arg)
		if tt.wantErr {
			if err == nil {
				t.Errorf("resolveTarget(%q) expected error, got nil", tt.arg)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveTarget(%q) error: %v", tt.arg, err)
			continue
		}
		if got.dialAddr != tt.dialAddr {
			t.Errorf("resolveTarget(%q) dialAddr = %q, want %q", tt.arg, got.dialAddr, tt.dialAddr)
		}
		if got.password != tt.password {
			t.Errorf("resolveTarget(%q) password = %q, want %q", tt.arg, got.password, tt.password)
		}
		if got.db != tt.db {
			t.Errorf("resolveTarget(%q) db = %d, want %d", tt.arg, got.db, tt.db)
		}
		if got.tls != tt.tls {
			t.Errorf("resolveTarget(%q) tls = %v, want %v", tt.arg, got.tls, tt.tls)
		}
	}
}
