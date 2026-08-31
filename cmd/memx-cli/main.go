// Command memx-cli is a redis-cli-style client for mem-x: interactive mode
// (a prompt that reads commands line by line) and one-shot mode (command given
// as arguments). Every request is timed and reported in ms. Server error
// replies and connection issues are surfaced on stderr. Stdlib-only runtime
// (AGENTS.md §5).
//
// The server may be given as a host:port or as a memx:// connection URL:
//
//	memx://[[user]:[password]@]host[:port][/db]
//
// A URL as the first argument is treated as the address and the remaining
// arguments as the command. Passwords and DB indices from the URL are applied
// (AUTH / SELECT) after connecting.
//
// Auto-spawn: when the server is unreachable and the address is local
// (127.0.0.1, localhost, :6379, etc.), the CLI boots an in-process mem-x
// server on that port, connects to it, and stops the server when the CLI
// exits. Data is ephemeral and lost on exit.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"mem-x/internal/cli"
	"mem-x/internal/memxurl"
	"mem-x/internal/resp"
)

// connTarget is the resolved server address: either a plain host:port or the
// components extracted from a memx:// / memxs:// URL.
type connTarget struct {
	dialAddr string // host:port used for the TCP dial
	password string // from URL userinfo ("" when absent)
	db       int    // from URL path (0 when absent)
	tls      bool   // memxs://
}

// resolveTarget parses the CLI's address argument. A plain host:port is used
// as-is; a memx:// / memxs:// URL is decomposed into dial address, password,
// and DB index. An unknown scheme (http://, redis://, …) is rejected instead
// of being silently treated as a host string.
func resolveTarget(arg string) (connTarget, error) {
	if !memxurl.IsURL(arg) {
		if strings.Contains(arg, "://") {
			return connTarget{}, fmt.Errorf("unsupported scheme in %q (want memx:// or memxs://)", arg)
		}
		return connTarget{dialAddr: arg}, nil
	}
	u, err := memxurl.Parse(arg)
	if err != nil {
		return connTarget{}, err
	}
	return connTarget{
		dialAddr: u.Addr(),
		password: u.Password,
		db:       u.DB,
		tls:      u.TLS,
	}, nil
}

// applyConnOptions performs the URL-declared AUTH and SELECT after the TCP
// connection is established, so the session starts in the requested context.
func applyConnOptions(c *cli.Client, t connTarget) error {
	if t.password != "" {
		reply, _, err := c.Do([]byte("AUTH"), []byte(t.password))
		if err != nil {
			return fmt.Errorf("AUTH: %w", err)
		}
		if reply.Kind == resp.RError {
			return fmt.Errorf("AUTH rejected: %s", reply.Str)
		}
	}
	if t.db != 0 {
		reply, _, err := c.Do([]byte("SELECT"), []byte(strconv.Itoa(t.db)))
		if err != nil {
			return fmt.Errorf("SELECT: %w", err)
		}
		if reply.Kind == resp.RError {
			return fmt.Errorf("SELECT rejected: %s", reply.Str)
		}
	}
	return nil
}

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "server address: host:port or memx:// URL (auto-spawns if local and unreachable)")
	timeout := flag.Duration("timeout", 5*time.Second, "connection timeout")
	flag.Parse()

	// A URL as the first positional argument is the server address; the rest
	// of the arguments are the one-shot command.
	targetArg := *addr
	rest := flag.Args()
	if len(rest) > 0 && memxurl.IsURL(rest[0]) {
		targetArg = rest[0]
		rest = rest[1:]
	}

	t, err := resolveTarget(targetArg)
	if err != nil {
		slog.Error("invalid address", "addr", targetArg, "err", err)
		os.Exit(1)
	}
	if t.tls {
		slog.Error("memxs:// (TLS) is not supported yet")
		os.Exit(1)
	}

	connectedAddr := t.dialAddr
	c, emb, err := dialOrSpawn(t.dialAddr, *timeout)
	if err != nil {
		slog.Error("cannot connect", "addr", targetArg, "err", err)
		os.Exit(1)
	}
	defer c.Close()
	if emb != nil {
		connectedAddr = emb.Addr()
		fmt.Fprintln(os.Stderr, "note: no server at "+targetArg+"; started an embedded mem-x at "+connectedAddr+" (data is lost on exit)")
		defer emb.Close()
	}

	if err := applyConnOptions(c, t); err != nil {
		slog.Error("connection setup failed", "err", err)
		os.Exit(1)
	}

	// One-shot mode: remaining args are the command.
	if len(rest) > 0 {
		runOneShot(c, rest)
		return
	}

	// Interactive mode.
	runInteractive(c, connectedAddr)
}

// dialOrSpawn tries to connect to addr. If the connection fails and the addr
// is local, it starts an embedded mem-x server on that address and re-dials.
func dialOrSpawn(addr string, timeout time.Duration) (*cli.Client, *cli.Embedded, error) {
	c, err := cli.Dial(addr, timeout)
	if err == nil {
		return c, nil, nil
	}
	if !cli.IsLocalAddr(addr) {
		return nil, nil, err
	}
	emb, serr := cli.StartEmbedded(addr)
	if serr != nil {
		return nil, nil, serr
	}
	// Retry dial against the embedded server (its addr may differ from the
	// requested one if, e.g., the port was reassigned during Listen).
	c, err = cli.Dial(emb.Addr(), timeout)
	if err != nil {
		emb.Close()
		return nil, nil, err
	}
	return c, emb, nil
}

// runOneShot sends a single command and prints its reply (plus latency).
func runOneShot(c *cli.Client, args []string) {
	bargs := make([][]byte, len(args))
	for i, a := range args {
		bargs[i] = []byte(a)
	}
	reply, d, err := c.Do(bargs...)
	if err != nil {
		slog.Error("command failed", "err", err)
		os.Exit(1)
	}
	fmt.Println(cli.FormatReply(reply))
	fmt.Println(cli.FormatLatency(d))
}

// runInteractive reads commands from stdin until EOF or QUIT/EXIT.
func runInteractive(c *cli.Client, addr string) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	prompt := addr + "> "
	for {
		fmt.Print(prompt)
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch strings.ToUpper(line) {
		case "QUIT", "EXIT":
			return
		case "HELP":
			printHelp()
			continue
		}
		tokens := cli.Tokenize(line)
		bargs := make([][]byte, len(tokens))
		for i, t := range tokens {
			bargs[i] = []byte(t)
		}
		reply, d, err := c.Do(bargs...)
		if err != nil {
			fmt.Println("(connection error) " + err.Error())
			return
		}
		fmt.Println(cli.FormatReply(reply))
		fmt.Println(cli.FormatLatency(d))
	}
}

func printHelp() {
	fmt.Println("memx-cli — a redis-cli-style client for mem-x")
	fmt.Println("  Type a command (e.g. SET k v), then press Enter.")
	fmt.Println("  QUIT/EXIT to leave. HELP for this text.")
	fmt.Println("  Each reply is followed by its round-trip latency in ms.")
	fmt.Println("  If the server is not running, the CLI starts one embedded.")
	fmt.Println("  Address: host:port or memx://[[user]:[pass]@]host[:port][/db]")
}
