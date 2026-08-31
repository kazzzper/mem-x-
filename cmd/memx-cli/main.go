// Command memx-cli is a redis-cli-style client for mem-x: interactive mode
// (a prompt that reads commands line by line) and one-shot mode (command given
// as arguments). Every request is timed and reported in ms. Server error
// replies and connection issues are surfaced on stderr. Stdlib-only runtime
// (AGENTS.md §5).
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
	addr := flag.String("addr", "127.0.0.1:6379", "server host:port")
	timeout := flag.Duration("timeout", 5*time.Second, "connection timeout")
	noPrompt := flag.Bool("no-raw", false, "do not print the per-request latency suffix")
	flag.Parse()

	if *noPrompt {
		// reserved; kept for CLI compatibility
	}

	c, err := cli.Dial(*addr, *timeout)
	if err != nil {
		slog.Error("cannot connect", "addr", *addr, "err", err)
		os.Exit(1)
	}
	defer c.Close()

	// One-shot mode: remaining args are the command.
	if rest := flag.Args(); len(rest) > 0 {
		runOneShot(c, rest)
		return
	}

	// Interactive mode.
	runInteractive(c, *addr)
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
}
