package cli

import (
	"testing"
	"time"

	"mem-x/internal/resp"
)

func TestIsLocalAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{":6379", true},
		{"127.0.0.1:6379", true},
		{"localhost:6379", true},
		{"::1:6379", true},
		{"0.0.0.0:6379", true},
		{"redis.example.com:6379", false},
		{"10.0.0.5:6379", false},
		{"192.168.1.10:6379", false},
	}
	for _, tc := range tests {
		if got := IsLocalAddr(tc.addr); got != tc.want {
			t.Errorf("IsLocalAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestStartEmbedded(t *testing.T) {
	emb, err := StartEmbedded("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer emb.Close()

	// The server must accept a real RESP client on the bound port.
	c, err := Dial(emb.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial embedded %s: %v", emb.Addr(), err)
	}
	defer c.Close()

	reply, _, err := c.Do([]byte("PING"))
	if err != nil {
		t.Fatal(err)
	}
	if reply.Kind != resp.Simple || string(reply.Str) != "PONG" {
		t.Fatalf("PING reply = %v, want Simple PONG", reply)
	}

	// Round-trip a write.
	if _, _, err := c.Do([]byte("SET"), []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	reply, _, err = c.Do([]byte("GET"), []byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if reply.Kind != resp.Bulk || string(reply.Str) != "v" {
		t.Fatalf("GET reply = %v, want Bulk v", reply)
	}
}

// TestEmbeddedShutdown verifies Close() tears the server down so the port no
// longer accepts connections.
func TestEmbeddedShutdown(t *testing.T) {
	emb, err := StartEmbedded("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := emb.Addr()
	emb.Close()

	// A dial to the closed server must fail within the timeout.
	if _, err := Dial(addr, 200*time.Millisecond); err == nil {
		t.Fatalf("dial after Close succeeded, want error (addr %s)", addr)
	}
}
