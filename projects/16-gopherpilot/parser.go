package gopherpilot

import "errors"

// Parser errors. ErrTruncated means the stream ended with a non-empty
// partial line (no terminating newline); ErrBadLine means a complete line
// failed to decode as a StreamEvent.
var (
	ErrTruncated = errors.New("gopherpilot: stream truncated mid-line")
	ErrBadLine   = errors.New("gopherpilot: malformed stream line")
)

// StreamParser incrementally reassembles NDJSON stream events from
// arbitrarily split chunks — including splits in the middle of tokens or
// JSON escape sequences. Only '\n'-terminated lines are decoded; Finish
// rejects any leftover partial data.
type StreamParser struct {
	// TODO: buffered bytes + decoded events.
}

// NewStreamParser returns an empty parser.
func NewStreamParser() *StreamParser {
	// TODO: return ready parser.
	return nil
}

// Feed appends a chunk (any size, any split points) and decodes every
// complete line it completes.
func (p *StreamParser) Feed(chunk string) error {
	// TODO: buffer, extract '\n'-terminated lines, decode each.
	return nil
}

// Finish verifies the stream ended on a clean line boundary.
func (p *StreamParser) Finish() error {
	// TODO: error if buffered partial line remains.
	return nil
}

// Events returns all events decoded so far, in order.
func (p *StreamParser) Events() []StreamEvent {
	// TODO: return accumulated events.
	return nil
}
