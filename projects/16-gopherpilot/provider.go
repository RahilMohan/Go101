// Package gopherpilot is the Go101 capstone: a TUI coding agent.
//
// Deliberate design note (UI independence): charmbracelet/bubbletea is not
// available in the module cache, so the agent core is implemented as a thin
// stdlib-only engine (a bufio line-driven REPL loop with ANSI status
// rendering are possible on top) behind interfaces — Provider for LLMs and
// plain function tools for actions. The core logic never touches a terminal,
// so any TUI framework can be layered on later without changing this package.
package gopherpilot

import (
	"context"
	"errors"
	"net/http"
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
	// TODO: read os.Getenv(APIKeyEnv); empty → ErrMissingAPIKey.
	return nil, nil
}

// Stream POSTs the request and streams NDJSON lines through a StreamParser
// into the returned channel.
func (p *RealProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	// TODO: implement real streaming; untested against the network here.
	return nil, nil
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
	// TODO: store scripts.
	return &FakeProvider{}
}

// Stream hands out the next scripted event stream.
func (f *FakeProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	// TODO: record req; pop next script into a closed-when-full channel.
	return nil, nil
}

// RecordedRequests returns copies of all requests seen so far.
func (f *FakeProvider) RecordedRequests() []Request {
	// TODO: return defensive copy.
	return nil
}

// scriptExhausted is returned when Stream is called more times than scripted.
var scriptExhausted = errors.New("fakeprovider: script queue exhausted")
