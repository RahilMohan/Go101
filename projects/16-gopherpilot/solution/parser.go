package gopherpilot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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
	buf    []byte
	events []StreamEvent
	lines  int // complete lines extracted so far
}

// NewStreamParser returns an empty parser.
func NewStreamParser() *StreamParser {
	return &StreamParser{}
}

// Feed appends a chunk (any size, any split points) and decodes every
// complete line it completes.
func (p *StreamParser) Feed(chunk string) error {
	p.buf = append(p.buf, chunk...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			return nil
		}
		line := string(p.buf[:i])
		p.buf = p.buf[i+1:]
		// Tolerate a UTF-8 BOM prepended by some HTTP endpoints; only the
		// very first line can carry it.
		if p.lines == 0 {
			line = strings.TrimPrefix(line, "\xEF\xBB\xBF")
		}
		p.lines++
		if err := p.parseLine(line); err != nil {
			return err
		}
	}
}

// parseLine decodes one complete NDJSON line into an event.
func (p *StreamParser) parseLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var ev StreamEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		brief := line
		if len(brief) > 32 {
			brief = brief[:29] + "..."
		}
		return fmt.Errorf("%w %q: %v", ErrBadLine, brief, err)
	}
	switch ev.Type {
	case EventText, EventToolCall, EventFinal:
	default:
		return fmt.Errorf("%w: unknown event type %q", ErrBadLine, ev.Type)
	}
	p.events = append(p.events, ev)
	return nil
}

// Finish verifies the stream ended on a clean line boundary.
func (p *StreamParser) Finish() error {
	if len(bytes.TrimSpace(p.buf)) > 0 {
		return fmt.Errorf("%w (%d trailing bytes)", ErrTruncated, len(p.buf))
	}
	return nil
}

// Events returns all events decoded so far, in order.
func (p *StreamParser) Events() []StreamEvent {
	return p.events
}
