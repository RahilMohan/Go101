// Command loglens is a streaming access-log analyzer CLI.
//
// Flags: -n <int> (top N paths, default 10), -status <code> (filter the
// by-status table to a single code). Arguments are file paths (multiple
// allowed); with no arguments it reads stdin.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	solution "go101.dev/projects/03-loglens/solution"
)

func main() {
	topN := flag.Int("n", 10, "number of top paths to show")
	statusFilter := flag.Int("status", 0, "show only this status code (0 = all)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		if !run("<stdin>", os.Stdin, *topN, *statusFilter) {
			os.Exit(1)
		}
		return
	}

	failed := false
	for _, name := range args {
		f, err := os.Open(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "loglens:", err)
			failed = true
			continue
		}
		ok := run(name, f, *topN, *statusFilter)
		f.Close()
		if !ok {
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// run analyzes one input and prints its report. It returns false only for
// hard failures (unreadable input); a *LineError with partial stats is
// reported as a warning and still counts as success (exit 0).
func run(name string, r io.Reader, topN, statusFilter int) bool {
	stats, err := solution.Analyze(r)
	if stats != nil {
		printReport(stats, topN, statusFilter)
	}
	if err == nil {
		return true
	}
	var le *solution.LineError
	if errors.As(err, &le) {
		fmt.Fprintf(os.Stderr, "loglens: %s: warning: %v\n", name, err)
		return stats != nil // partial-stats contract
	}
	fmt.Fprintln(os.Stderr, "loglens:", name+":", err)
	return false
}

func printReport(s *solution.Stats, topN, statusFilter int) {
	fmt.Printf("parsed: %d skipped: %d\n", s.Total, s.Skipped)

	fmt.Println("top paths:")
	for _, pc := range solution.TopPaths(s, topN) {
		fmt.Printf("  %s  %d\n", pc.Path, pc.Count)
	}

	fmt.Println("by status:")
	codes := make([]int, 0, len(s.ByStatus))
	for code := range s.ByStatus {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	for _, code := range codes {
		if statusFilter > 0 && code != statusFilter {
			continue
		}
		fmt.Printf("  %d  %d\n", code, s.ByStatus[code])
	}
}
