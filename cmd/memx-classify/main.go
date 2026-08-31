// Command memx-classify grades a task and emits the classifier routing line
// (AGENTS.md §3, agents/classifier.md output contract):
//
//	task=<id> complexity=<S|M|L|XL> type=<...> agent=<id> model=<tier> reason=<short>
//
// Usage:
//
//	memx-classify [id] [task...]     # grade one task (task = remaining args or stdin)
//	memx-classify -registry          # list the type→agent registry
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"mem-x/internal/agent"
)

func main() {
	list := flag.Bool("registry", false, "print the type→agent registry and exit")
	flag.Parse()

	if *list {
		printRegistry()
		return
	}

	// Task comes from args; if none, read stdin (one task per line).
	if flag.NArg() >= 2 {
		id, task := flag.Arg(0), strings.Join(flag.Args()[1:], " ")
		fmt.Println(agent.Classify(id, task).Line())
		return
	}

	sc := bufio.NewScanner(os.Stdin)
	for i := 1; sc.Scan(); i++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		id := fmt.Sprintf("%d", i)
		fmt.Println(agent.Classify(id, line).Line())
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "memx-classify: read stdin:", err)
		os.Exit(1)
	}
}

func printRegistry() {
	types := make([]agent.TaskType, 0, len(agent.Registry))
	for t := range agent.Registry {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	fmt.Println("task-type → agent (AGENTS.md §1)")
	for _, t := range types {
		fmt.Printf("  %-12s → %s\n", t, agent.Registry[t])
	}
	fmt.Printf("  %-12s → <classifier decides complexity> → model tier via AGENTS.md §3\n", "complexity")
}
