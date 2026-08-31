// Command memx-url builds a memx:// or memxs:// connection URL from the same
// MEMX_* environment variables the mem-x server reads. It is the companion to
// the Docker setup: point it at the same environment as the container and it
// prints a URL you can hand to memx-cli verbatim (password percent-encoded).
//
// Variables (all optional; defaults match the server):
//
//	MEMX_HOST      hostname            (default: localhost)
//	MEMX_PORT      port                (default: 6379, or 6380 when MEMX_TLS=1)
//	MEMX_USER      URL user            (default: none)
//	MEMX_PASSWORD  requirepass/password
//	MEMX_TLS       "1" emits memxs://  (default: 0 -> memx://)
//	MEMX_DB        database index in the URL path
//
// Usage:
//
//	MEMX_PASSWORD=secret ./memx-url
//	MEMX_TLS=1 MEMX_PASSWORD=secret ./memx-url
package main

import (
	"fmt"
	"os"

	"mem-x/internal/memxurl"
)

func main() {
	host := getenv("MEMX_HOST", "localhost")
	port := os.Getenv("MEMX_PORT")
	user := os.Getenv("MEMX_USER")
	password := os.Getenv("MEMX_PASSWORD")
	db := atoi(getenv("MEMX_DB", "0"))

	scheme := "memx"
	if getenv("MEMX_TLS", "0") == "1" {
		scheme = "memxs"
	}

	u := memxurl.New(scheme, host, port, user, password, db)
	fmt.Println(u.ConnString())
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
