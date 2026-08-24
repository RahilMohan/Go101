// Command wordkit is a wc-style text analyzer CLI.
//
// Flags select which columns to print; with no flags all four are printed.
// Arguments are file paths (multiple allowed); with no arguments it reads
// stdin.
package main

import (
	"flag"
	"fmt"
	"os"

	solution "go101.dev/projects/01-wordkit/solution"
)

func main() {
	lines := flag.Bool("l", false, "print the newline count")
	words := flag.Bool("w", false, "print the word count")
	bytes := flag.Bool("c", false, "print the byte count")
	chars := flag.Bool("m", false, "print the character count")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		stats, err := solution.Analyze(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "wordkit: stdin:", err)
			os.Exit(1)
		}
		fmt.Println(formatColumns(*lines, *words, *bytes, *chars, stats))
		return
	}

	failed := false
	for _, name := range args {
		f, err := os.Open(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "wordkit:", err)
			failed = true
			continue
		}
		stats, err := solution.Analyze(f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, "wordkit:", name+":", err)
			failed = true
			continue
		}
		fmt.Println(formatColumns(*lines, *words, *bytes, *chars, stats) + " " + name)
	}
	if failed {
		os.Exit(1)
	}
}

func formatColumns(l, w, c, m bool, s solution.Stats) string {
	if !l && !w && !c && !m {
		return solution.Format(s)
	}
	var out string
	if l {
		out += fmt.Sprintf("%8d", s.Lines)
	}
	if w {
		out += fmt.Sprintf("%8d", s.Words)
	}
	if c {
		out += fmt.Sprintf("%8d", s.Bytes)
	}
	if m {
		out += fmt.Sprintf("%8d", s.Chars)
	}
	return out
}
