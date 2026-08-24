// Command gopherfetch checks URLs concurrently and prints one line per
// result:
//
//	OK   200  12ms  https://x
//	FAIL 0    3ms   https://y  Get "https://y": connection refused
//
// Flags: -c <workers> (default 8), -timeout <dur> (default 10s),
// -rate <dur> (0 = off). Exit status is always 0 unless flag parsing
// fails (2) — a failed link check is still a successful run of the tool.
// SIGINT cancels the in-flight context and prints the partial summary of
// completed checks.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	gopherfetch "go101.dev/projects/05-gopherfetch/solution"
)

func main() {
	os.Exit(run())
}

func run() int {
	workers := flag.Int("c", 8, "number of concurrent workers")
	timeout := flag.Duration("timeout", 10*time.Second, "per-request timeout")
	rate := flag.Duration("rate", 0, "minimum interval between request starts (0 = unlimited)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "gopherfetch: no URLs given")
		flag.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	checker := gopherfetch.NewChecker(*workers, *timeout)
	var results []gopherfetch.Result
	if *rate > 0 {
		results = checker.CheckRateLimited(ctx, flag.Args(), *rate)
	} else {
		results = checker.Check(ctx, flag.Args())
	}
	checker.CloseIdleConnections()

	failed := 0
	checked := 0 // results that actually completed a check, not ctx-abandoned ones
	for _, r := range results {
		dur := r.Duration.Truncate(time.Millisecond)
		if errors.Is(r.Err, context.Canceled) {
			fmt.Printf("SKIP %-4d %-6s %s  %v\n", r.Status, dur, r.URL, r.Err)
			continue
		}
		checked++
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL %-4d %-6s %s  %v\n", r.Status, dur, r.URL, r.Err)
			continue
		}
		fmt.Printf("OK   %-4d %-6s %s\n", r.Status, dur, r.URL)
	}

	fmt.Printf("\n%d/%d checked", checked, flag.NArg())
	if ctx.Err() != nil {
		fmt.Print(" (interrupted)")
	}
	fmt.Printf(", %d failed\n", failed)
	return 0
}
