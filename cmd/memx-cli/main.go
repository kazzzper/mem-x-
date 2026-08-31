// Command memx-cli is a redis-cli-style client for mem-x: interactive mode
// (a prompt that reads commands line by line) and one-shot mode (command given
// as arguments). Every request is timed and reported in ms. Server error
// replies and connection issues are surfaced on stderr. Stdlib-only runtime
// (AGENTS.md §5).
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
	"strings"
	"time"

	"mem-x/internal/cli"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "server host:port (auto-spawns if local and unreachable)")
	timeout := flag.Duration("timeout", 5*time.Second, "connection timeout")
	flag.Parse()

	connectedAddr := *addr
	c, emb, err := dialOrSpawn(*addr, *timeout)
	if err != nil {
		slog.Error("cannot connect", "addr", *addr, "err", err)
		os.Exit(1)
	}
	defer c.Close()
	if emb != nil {
		connectedAddr = emb.Addr()
		fmt.Fprintln(os.Stderr, "note: no server at "+*addr+"; started an embedded mem-x at "+connectedAddr+" (data is lost on exit)")
		defer emb.Close()
	}

	// One-shot mode: remaining args are the command.
	if rest := flag.Args(); len(rest) > 0 {
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
}
