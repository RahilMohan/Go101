// Package wordkit provides wc-style text analysis over io.Reader streams.
package wordkit

import (
	"io"
)

// Stats holds the counts computed by Analyze.
type Stats struct {
	Lines int64 // count of '\n' characters; a final line without '\n' still counts
	Words int64 // whitespace-separated fields (unicode.IsSpace)
	Bytes int64 // total bytes read
	Chars int64 // UTF-8 rune count; invalid bytes count as one char each (utf8.RuneError handling)
}

// Analyze reads r fully and returns Stats.
// Reading must be streaming (bufio or io.Copy buffer), never load-all via
// io.ReadAll.
func Analyze(r io.Reader) (Stats, error) {
	// TODO: Implement
	return Stats{}, nil
}

// Format returns wc-style output: four space-padded %8d columns in the order
// Lines, Words, Bytes, Chars.
func Format(s Stats) string {
	// TODO: Implement
	return ""
}
