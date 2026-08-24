// Package solution is the reference implementation of project 03
// (loglens): a streaming access-log analyzer. The log-line regexp is
// compiled exactly once at package level — recompiling inside Analyze
// would dominate runtime and is the core lesson of this project.
package solution

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrSkip is the sentinel reported (via *LineError wrapping) when a
	// line does not match the log format. Skipped lines do not abort
	// analysis; they only increment Stats.Skipped.
	ErrSkip = errors.New("loglens: line does not match format")
	// ErrMalformed is the sentinel for lines that have the shape of a
	// log entry but contain unparseable fields (e.g. non-numeric status).
	ErrMalformed = errors.New("loglens: malformed line")
)

// maxLineSize raises the Scanner limit so long URLs in real logs are not
// dropped with bufio.ErrTooLong (the default cap is 64KB).
const maxLineSize = 1024 * 1024

// lineRe matches the common/combined format:
//
//	remote - user [timestamp] "METHOD path PROTO" status bytes
//
// Status and bytes stay as raw strings so unparseable fields can be
// reported as ErrMalformed (with the strconv error wrapped) instead of
// being silently skipped.
var lineRe = regexp.MustCompile(`^(\S+) \S+ (\S+) \[([^\]]*)\] "([A-Z]+) (\S+) [^"]*" (\S+) (\S+)$`)

// Entry is a parsed common/uncombined log-format line.
type Entry struct {
	Remote string
	User   string
	Method string
	Path   string
	Status int
	Bytes  int64 // "-" -> 0
}

// LineError identifies the 1-based line number that failed.
type LineError struct {
	Line int   // 1-based
	Err  error // ErrSkip or a wrapped parse failure
}

func (e *LineError) Error() string {
	return fmt.Sprintf("loglens: line %d: %v", e.Line, e.Err)
}

func (e *LineError) Unwrap() error { return e.Err }

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
// *LineError wrapping both ErrMalformed and the strconv failure; the
// returned partial stats reflect everything parsed before that line.
func Analyze(r io.Reader) (*Stats, error) {
	stats := &Stats{
		ByStatus: make(map[int]int),
		ByPath:   make(map[string]int),
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLineSize)

	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := strings.TrimSuffix(sc.Text(), "\r") // tolerate CRLF endings

		m := lineRe.FindStringSubmatch(line)
		if m == nil {
			stats.Skipped++ // ErrSkip: keep going, never abort
			continue
		}

		status, err := strconv.Atoi(m[6])
		if err != nil {
			return stats, &LineError{Line: lineNum, Err: fmt.Errorf("%w: status %q: %w", ErrMalformed, m[6], err)}
		}
		if m[7] != "-" {
			if _, err := strconv.ParseInt(m[7], 10, 64); err != nil {
				return stats, &LineError{Line: lineNum, Err: fmt.Errorf("%w: bytes %q: %w", ErrMalformed, m[7], err)}
			}
		}

		stats.Total++
		stats.ByStatus[status]++
		stats.ByPath[m[5]]++
	}
	if err := sc.Err(); err != nil {
		return stats, fmt.Errorf("loglens: scan: %w", err)
	}
	return stats, nil
}

// ParseEntry returns the Entry parsed from a single log-format line,
// reporting false when the line does not match the format. Exposed for
// learners to inspect individual fields without aggregation.
func ParseEntry(line string) (Entry, bool) {
	line = strings.TrimSuffix(line, "\r")
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return Entry{}, false
	}
	status, err := strconv.Atoi(m[6])
	if err != nil {
		return Entry{}, false
	}
	var nbytes int64
	if m[7] != "-" {
		nbytes, err = strconv.ParseInt(m[7], 10, 64)
		if err != nil {
			return Entry{}, false
		}
	}
	return Entry{
		Remote: m[1],
		User:   m[2],
		Method: m[4],
		Path:   m[5],
		Status: status,
		Bytes:  nbytes,
	}, true
}

// PathCount is one path with its hit count, as returned by TopPaths.
type PathCount struct {
	Path  string
	Count int
}

// TopPaths returns up to n paths sorted by count desc, then path asc for
// ties. If n exceeds the number of distinct paths it is clamped.
func TopPaths(s *Stats, n int) []PathCount {
	if s == nil || n <= 0 {
		return []PathCount{}
	}
	counts := make([]PathCount, 0, len(s.ByPath))
	for p, c := range s.ByPath {
		counts = append(counts, PathCount{Path: p, Count: c})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count != counts[j].Count {
			return counts[i].Count > counts[j].Count
		}
		return counts[i].Path < counts[j].Path
	})
	if len(counts) > n {
		counts = counts[:n]
	}
	return counts
}

// CompilePattern compiles the package's internal log-line regexp. The
// regexp itself is compiled ONCE at package level; this accessor never
// recompiles — it exists so learners can benchmark it against hand-rolled
// parsing.
func CompilePattern() *regexp.Regexp { return lineRe }
