// Package loglens provides streaming analysis of Apache/nginx-style
// access logs (common/combined format) using a single compiled regexp,
// bufio.Scanner streaming, and error wrapping with sentinel errors.
package loglens

import (
	"errors"
	"io"
	"regexp"
)

// Entry is a parsed common/uncombined log-format line
// (Apache/nginx style):
//
//	remote - user [timestamp] "METHOD path PROTO" status bytes
type Entry struct {
	Remote string
	User   string
	Method string
	Path   string
	Status int
	Bytes  int64 // "-" -> 0
}

var (
	// ErrSkip is the sentinel reported (via *LineError wrapping) when a
	// line does not match the log format. Skipped lines do not abort
	// analysis; they only increment Stats.Skipped.
	ErrSkip = errors.New("loglens: line does not match format")
	// ErrMalformed is the sentinel for lines that have the shape of a
	// log entry but contain unparseable fields (e.g. non-numeric status).
	ErrMalformed = errors.New("loglens: malformed line")
)

// LineError identifies the 1-based line number that failed.
type LineError struct {
	Line int   // 1-based
	Err  error // ErrSkip or a wrapped parse failure
}

func (e *LineError) Error() string {
	// TODO: Implement
	return ""
}

func (e *LineError) Unwrap() error {
	// TODO: Implement
	return nil
}

// Stats aggregates counts over parsed lines.
type Stats struct {
	Total    int            // successfully parsed lines
	Skipped  int            // non-matching lines (ErrSkip)
	ByStatus map[int]int    // status code -> count
	ByPath   map[string]int // raw Path -> count
}

// Analyze parses r line-by-line using bufio.Scanner with a raised buffer
// limit (1MB lines). It NEVER loads the whole input into memory.
//
// Non-matching lines increment Skipped and do not abort analysis.
// Malformed matching-shaped lines (e.g. non-numeric status) abort with a
// *LineError wrapping ErrMalformed and the strconv failure; the returned
// partial stats reflect everything parsed before that line.
func Analyze(r io.Reader) (*Stats, error) {
	// TODO: Implement
	return nil, nil
}

// PathCount is one path with its hit count, as returned by TopPaths.
type PathCount struct {
	Path  string
	Count int
}

// TopPaths returns up to n paths sorted by count desc, then path asc for
// ties. If n exceeds the number of distinct paths it is clamped.
func TopPaths(s *Stats, n int) []PathCount {
	// TODO: Implement
	return nil
}

// CompilePattern compiles the package's internal log-line regexp. It is
// exported so learners can benchmark it against hand-rolled parsing. The
// regexp itself is compiled ONCE at package level; this accessor never
// recompiles.
func CompilePattern() *regexp.Regexp {
	// TODO: Implement
	return nil
}
