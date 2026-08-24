// Package chatwire provides a multi-client TCP chat server, teaching
// raw sockets (net), reader-per-connection goroutines, fan-out through a
// hub that owns its subscriber map, slow-subscriber/slow-client
// isolation via bounded channels with drop semantics, and graceful
// shutdown under a context deadline.
package chatwire

import (
	"bufio"
	"context"
	"io"
)

// Event is emitted by the hub to subscribers.
type Event struct {
	From    string // display name; "" for system events
	Kind    string // "join" | "msg" | "leave" | "system"
	Payload string
}

// Server is a multi-client chat server.
type Server struct {
	// TODO: Implement
	_ struct{}
}

// NewServer returns a ready-to-serve Server.
func NewServer() *Server {
	// TODO: Implement
	return nil
}

// Serve listens on addr and blocks until ln closes or Shutdown is called.
// Each accepted connection spawns a reader goroutine (reader-per-connection).
func (s *Server) Serve(addr string) error {
	// TODO: Implement
	return nil
}

// Shutdown stops accepting, politely disconnects clients (writes a system
// goodbye line then closes), and unblocks Serve. Safe to call once; second
// call returns net.ErrClosed wrapped. If ctx expires before every client
// write completes, remaining connections are force-closed and ctx.Err() is
// returned.
func (s *Server) Shutdown(ctx context.Context) error {
	// TODO: Implement
	return nil
}

// Subscribe returns a channel receiving every event until Unsubscribe or
// server close. The channel is buffered with capacity buf; the hub DROPS
// events (never blocks) when the buffer is full — slow-subscriber isolation.
func (s *Server) Subscribe(buf int) <-chan Event {
	// TODO: Implement
	return nil
}

// Unsubscribe removes ch from the hub's subscriber set.
func (s *Server) Unsubscribe(ch <-chan Event) {
	// TODO: Implement
}

// WriteLine writes s followed by a newline.
func WriteLine(w io.Writer, s string) error {
	// TODO: Implement
	return nil
}

// ReadLine reads one newline-terminated line, trimming the terminator.
// It returns io.EOF when the peer closed cleanly.
func ReadLine(r *bufio.Reader) (string, error) {
	// TODO: Implement
	return "", nil
}
