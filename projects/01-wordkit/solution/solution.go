package solution

import (
	"bufio"
	"fmt"
	"io"
	"unicode"
)

// Stats holds the counts computed by Analyze.
type Stats struct {
	Lines int64 // count of '\n' characters; a final line without '\n' still counts
	Words int64 // whitespace-separated fields (unicode.IsSpace)
	Bytes int64 // total bytes read
	Chars int64 // UTF-8 rune count; invalid bytes count as one char each (utf8.RuneError handling)
}

// Analyze reads r fully and returns Stats. It streams the input through a
// fixed-size buffer, so memory use is constant regardless of input size.
func Analyze(r io.Reader) (Stats, error) {
	var s Stats
	var (
		inWord      bool
		pendingLine bool // non-empty content after the last '\n'
	)

	br := bufio.NewReader(r)
	for {
		c, size, err := br.ReadRune()
		if err != nil {
			if err == io.EOF {
				break
			}
			return s, fmt.Errorf("wordkit: read: %w", err)
		}
		s.Bytes += int64(size)
		s.Chars++ // invalid bytes decode as utf8.RuneError with size 1: still one char each

		if c == '\n' {
			s.Lines++
			inWord = false
			pendingLine = false
			continue
		}
		pendingLine = true
		if unicode.IsSpace(c) {
			inWord = false
			continue
		}
		if !inWord {
			s.Words++
			inWord = true
		}
	}

	// A final line without a trailing '\n' still counts.
	if pendingLine {
		s.Lines++
	}
	return s, nil
}

// Format returns wc-style output: four space-padded %8d columns in the order
// Lines, Words, Bytes, Chars.
func Format(s Stats) string {
	return fmt.Sprintf("%8d%8d%8d%8d", s.Lines, s.Words, s.Bytes, s.Chars)
}
