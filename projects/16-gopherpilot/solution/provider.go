package gopherpilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
)

// Role identifies who authored a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in the conversation history.
type Message struct {
	Role    Role
	Content string
}

// StreamEventType discriminates StreamEvent values.
type StreamEventType string

const (
	EventText     StreamEventType = "text"      // incremental assistant text
	EventToolCall StreamEventType = "tool_call" // model requests a tool run
	EventFinal    StreamEventType = "final"     // final answer; loop may stop
	EventError    StreamEventType = "error"     // terminal stream failure (wrapped parse/read error)
)

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// StreamEvent is one decoded item from a provider stream.
type StreamEvent struct {
	Type StreamEventType `json:"type"`
	Text string          `json:"text,omitempty"`
	Call *ToolCall       `json:"call,omitempty"`
}

// Request carries conversation state to a Provider.
type Request struct {
	Messages []Message
}

// Provider abstracts an LLM backend. Stream returns a channel that yields
// events until closed. The LLM lives strictly behind this interface so tests
// use scripted fakes and no network.
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

// ErrMissingAPIKey is returned by NewRealProvider when GOPHERPILOT_API_KEY
// is unset or empty. The key value itself must never be logged anywhere.
var ErrMissingAPIKey = errors.New("gopherpilot: GOPHERPILOT_API_KEY not set")

// APIKeyEnv is the only sanctioned source of credentials.
const APIKeyEnv = "GOPHERPILOT_API_KEY"

// RealProvider talks to a real HTTP streaming endpoint. Construction fails
// fast if the API key env var is missing; the key is stored, never logged.
type RealProvider struct {
	endpoint string
	apiKey   string // never log
	client   *http.Client
}

// NewRealProvider builds a RealProvider against endpoint, reading the API
// key strictly from the environment.
func NewRealProvider(endpoint string) (*RealProvider, error) {
	key := os.Getenv(APIKeyEnv)
	if key == "" {
		return nil, fmt.Errorf("%w", ErrMissingAPIKey)
	}
	return &RealProvider{
		endpoint: endpoint,
		apiKey:   key,
		client:   http.DefaultClient,
	}, nil
}

// wireRequest is the JSON body sent to the endpoint.
type wireRequest struct {
	Messages []Message `json:"messages"`
}

// Stream POSTs the request and streams NDJSON lines through a StreamParser
// into the returned channel.
func (p *RealProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	body, err := json.Marshal(wireRequest{Messages: req.Messages})
	if err != nil {
		return nil, fmt.Errorf("gopherpilot: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gopherpilot: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gopherpilot: stream request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("gopherpilot: stream status %d", resp.StatusCode)
	}

	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		parser := NewStreamParser()
		sent := 0
		push := func() bool {
			evs := parser.Events()
			for ; sent < len(evs); sent++ {
				select {
				case ch <- evs[sent]:
				case <-ctx.Done():
					return false
				}
			}
			return true
		}
		sc := bufio.NewScanner(resp.Body)
		// Raise the line cap well above bufio's 64KB default so large
		// NDJSON events stream instead of silently truncating.
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		fail := func(err error) bool {
			select {
			case ch <- StreamEvent{Type: EventError, Text: err.Error()}:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for sc.Scan() {
			if err := parser.Feed(sc.Text() + "\n"); err != nil {
				_ = push() // flush events decoded before the failure
				fail(fmt.Errorf("gopherpilot: parse stream: %w", err))
				return
			}
			if !push() {
				return
			}
		}
		if err := sc.Err(); err != nil {
			_ = push()
			fail(fmt.Errorf("gopherpilot: read stream: %w", err))
			return
		}
		parser.Feed("\n") // flush is a no-op; boundary check only
		_ = push()
	}()
	return ch, nil
}

// FakeProvider replays scripted event streams: each Stream call consumes the
// next script. It records every request so tests can assert exactly what the
// agent fed back (e.g. wrapped tool results).
type FakeProvider struct {
	mu       sync.Mutex
	scripts  [][]StreamEvent
	next     int
	requests []Request
}

// NewFakeProvider returns a provider replaying scripts in call order.
func NewFakeProvider(scripts ...[]StreamEvent) *FakeProvider {
	return &FakeProvider{scripts: scripts}
}

// Stream hands out the next scripted event stream.
func (f *FakeProvider) Stream(_ context.Context, req Request) (<-chan StreamEvent, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	if f.next >= len(f.scripts) {
		f.mu.Unlock()
		return nil, scriptExhausted
	}
	script := f.scripts[f.next]
	f.next++
	f.mu.Unlock()

	ch := make(chan StreamEvent, len(script))
	for _, ev := range script {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// RecordedRequests returns copies of all requests seen so far.
func (f *FakeProvider) RecordedRequests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Request, len(f.requests))
	for i, r := range f.requests {
		out[i].Messages = append([]Message(nil), r.Messages...)
	}
	return out
}

// scriptExhausted is returned when Stream is called more times than scripted.
var scriptExhausted = errors.New("fakeprovider: script queue exhausted")
