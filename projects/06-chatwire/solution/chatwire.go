// Package solution is the reference implementation of project 06
// (chatwire): a multi-client TCP chat server. The lessons are raw sockets
// (net.Listener + net.Conn), reader-per-connection goroutines, fan-out via
// a single hub goroutine that owns all shared state (subscriber map and
// client registry — no mutexes on the hot path), slow-subscriber isolation
// (bounded subscriber channels with drop semantics) and slow-client
// isolation (per-connection 64-slot write queues; a full queue drops the
// client instead of stalling the hub), and graceful shutdown bounded by a
// context deadline with force-close fallback.
//
// Wire protocol: newline-delimited UTF-8 text. A client's first line is its
// requested display name; every later line is a chat message. Server→client
// lines are "name: message" for chat and "* text" for system events.
package solution

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	// clientOutBuf is the per-connection write-queue capacity. When the hub
	// cannot queue a line because this is full, the client is dropped:
	// slow-client isolation. We never block the hub on one bad connection.
	clientOutBuf = 64

	// hubQueueBuf bounds the hub's inbound event queue. Readers block (not
	// drop) on it, applying natural backpressure to fast talkers.
	hubQueueBuf = 256

	// handshakeTimeout bounds how long a connection may sit silent before
	// sending its display-name line, so pre-registration conns can't leak.
	handshakeTimeout = 30 * time.Second

	// idleTimeout bounds how long a registered connection may stay idle
	// without sending a line. Each successful ReadLine resets the deadline,
	// so an idle client is disconnected after this duration. This mitigates
	// slowloris-style holds that would otherwise keep a goroutine/FD alive
	// indefinitely.
	idleTimeout = 60 * time.Second

	// maxLineBytes caps the maximum wire line length (including newline).
	// Lines exceeding this are rejected and the client is disconnected,
	// preventing a single peer from forcing unbounded heap allocation (OOM).
	maxLineBytes = 4 << 20 // 4 MiB

	// maxConns caps concurrent accepted connections. When the limit is hit,
	// new connections are closed immediately, bounding goroutines and FDs.
	maxConns = 1000

	goodbyeText = "server shutting down, goodbye"
)

// Event is emitted by the hub to subscribers.
type Event struct {
	From    string // display name; "" for system events
	Kind    string // "join" | "msg" | "leave" | "system"
	Payload string
}

// WriteLine writes s followed by a newline.
func WriteLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%s\n", s)
	return err
}

// ReadLine reads one newline-terminated line, trimming \r\n. It returns
// io.EOF unchanged when the peer closed cleanly. Lines exceeding
// maxLineBytes are rejected with an error so the caller can disconnect the
// client, bounding per-connection allocation.
func ReadLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if len(line) > maxLineBytes {
		return "", fmt.Errorf("line too long: %d bytes exceeds %d", len(line), maxLineBytes)
	}
	return strings.TrimRight(line, "\r\n"), err
}

// formatEvent renders an Event as a wire line: "name: message" for chat,
// "* text" for everything system-flavored (join/leave/system).
func formatEvent(ev Event) string {
	if ev.Kind == "msg" {
		return ev.From + ": " + ev.Payload
	}
	return "* " + ev.Payload
}

// client is one registered connection. Its out channel has exactly two
// owners: the hub sends and closes it; the writer goroutine drains it and
// then closes the connection. Nobody else touches either.
type client struct {
	name string
	conn net.Conn
	out  chan string
}

type registerReq struct {
	name  string
	reply chan string // assigned (possibly suffixed) name
	c     *client
}

// Server is a multi-client chat server.
type Server struct {
	mu     sync.Mutex
	ln     net.Listener
	closed bool

	regCh    chan *registerReq
	unregCh  chan *client
	subAddCh chan chan Event
	subDelCh chan (<-chan Event)
	eventCh  chan Event
	stopCh   chan struct{}
	hubDone  chan struct{}

	wg     sync.WaitGroup // one unit per live writer goroutine
	doneCh chan struct{}  // closed when hub finished AND all writers exited

	connsMu   sync.Mutex
	liveConns map[net.Conn]struct{} // every registered conn, kept current incrementally so force-close never sees a stale snapshot

	sem chan struct{} // counting semaphore bounding concurrent connections to maxConns
}

// NewServer returns a ready-to-serve Server with its hub goroutine running.
func NewServer() *Server {
	s := &Server{
		regCh:     make(chan *registerReq),
		unregCh:   make(chan *client),
		subAddCh:  make(chan chan Event),
		subDelCh:  make(chan (<-chan Event)),
		eventCh:   make(chan Event, hubQueueBuf),
		stopCh:    make(chan struct{}),
		hubDone:   make(chan struct{}),
		doneCh:    make(chan struct{}),
		liveConns: make(map[net.Conn]struct{}),
		sem:       make(chan struct{}, maxConns),
	}
	go func() {
		<-s.hubDone
		s.wg.Wait()
		close(s.doneCh)
	}()
	go s.runHub()
	return s
}

// runHub is the single owner of the client registry and subscriber set.
// Nothing else touches those maps, so there are no locks on the hot path.
func (s *Server) runHub() {
	clients := make(map[string]*client)
	subs := make(map[<-chan Event]chan Event)

	for {
		select {
		case <-s.stopCh:
			s.finalize(clients, subs)
			return
		case req := <-s.regCh:
			s.register(clients, subs, req)
		case c := <-s.unregCh:
			s.removeAndAnnounce(clients, subs, c)
		case ch := <-s.subAddCh:
			subs[ch] = ch
		case ch := <-s.subDelCh:
			delete(subs, ch)
		case ev := <-s.eventCh:
			s.broadcast(clients, subs, ev)
		}
	}
}

// trackConn / untrackConn maintain the live-connection set used by
// force-close. Tracking happens incrementally at every registration and
// every removal point (drop, leave, finalize), so Shutdown's deadline path
// always sees the current conns even if ctx expires before the hub reaches
// finalize — there is no snapshot-timing hole. Closing an already-closed
// conn is a harmless no-op error.
func (s *Server) trackConn(cn net.Conn) {
	s.connsMu.Lock()
	s.liveConns[cn] = struct{}{}
	s.connsMu.Unlock()
}

func (s *Server) untrackConn(cn net.Conn) {
	s.connsMu.Lock()
	delete(s.liveConns, cn)
	s.connsMu.Unlock()
}

// register assigns a unique display name (suffixing duplicates bob → bob-2),
// starts the connection's writer goroutine, welcomes the newcomer and
// announces the join.
func (s *Server) register(clients map[string]*client, subs map[<-chan Event]chan Event, req *registerReq) {
	name := req.name
	for n := 2; clients[name] != nil; n++ {
		name = fmt.Sprintf("%s-%d", req.name, n)
	}
	c := req.c
	c.name = name
	clients[name] = c
	s.trackConn(c.conn)

	s.wg.Add(1)
	go s.writeLoop(c)

	req.reply <- name
	s.deliverLine(clients, c, "* you are "+name)
	if others := roster(clients, name); others != "" {
		s.deliverLine(clients, c, "* online: "+others)
	}
	if name != req.name {
		s.broadcast(clients, subs, Event{Kind: "system",
			Payload: fmt.Sprintf("%s is taken; new client named %s", req.name, name)})
	}
	s.broadcast(clients, subs, Event{Kind: "join", Payload: name + " joined"})
}

// roster lists every member except except, space-separated.
func roster(clients map[string]*client, except string) string {
	var names []string
	for n := range clients {
		if n != except {
			names = append(names, n)
		}
	}
	return strings.Join(names, " ")
}

// deliverLine queues one wire line for a client, dropping the client if its
// write queue is full (slow-client isolation).
func (s *Server) deliverLine(clients map[string]*client, c *client, line string) {
	select {
	case c.out <- line:
	default:
		// Slow client: its 64-slot queue is full. Drop rather than block the
		// whole room. The writer exits once out is closed; closing the raw
		// conn interrupts any in-flight Write immediately.
		delete(clients, c.name)
		s.untrackConn(c.conn)
		close(c.out)
		c.conn.Close()
	}
}

// broadcast fans an event out to every registered client's wire queue and to
// every subscriber channel. Subscriber sends are non-blocking: a full
// subscriber buffer loses events, never hub progress (slow-subscriber
// isolation). Deleting from clients during range is safe in Go.
func (s *Server) broadcast(clients map[string]*client, subs map[<-chan Event]chan Event, ev Event) {
	line := formatEvent(ev)
	for _, c := range clients {
		select {
		case c.out <- line:
		default:
			delete(clients, c.name)
			s.untrackConn(c.conn)
			close(c.out)
			c.conn.Close()
		}
	}
	for _, rw := range subs {
		select {
		case rw <- ev:
		default:
		}
	}
}

// removeAndAnnounce deregisters a leaving client and tells the room.
func (s *Server) removeAndAnnounce(clients map[string]*client, subs map[<-chan Event]chan Event, c *client) {
	if cur, ok := clients[c.name]; ok && cur == c {
		delete(clients, c.name)
		s.untrackConn(c.conn)
		close(c.out)
		s.broadcast(clients, subs, Event{Kind: "leave", Payload: c.name + " left"})
	}
}

// finalize performs the polite half of shutdown: a goodbye line for every
// client, then out-channel closes so each writer drains what it can and
// closes its conn. Subscriber channels are closed so their owners' range
// loops end. Conns stay in liveConns until force-close or natural writer
// exit handles them. The goodbye is best-effort: if a client's 64-slot
// queue is full the message is dropped and the drop is logged, so R4's
// "every client gets goodbye" holds under normal backpressure but may be
// missed by a wedged/slow client at shutdown time.
func (s *Server) finalize(clients map[string]*client, subs map[<-chan Event]chan Event) {
	for _, c := range clients {
		select {
		case c.out <- "* " + goodbyeText:
		default: // queue full: best-effort goodbye, log the drop
			log.Printf("chatwire: goodbye dropped for %s (queue full)", c.name)
		}
		close(c.out)
	}
	for _, rw := range subs {
		close(rw)
	}
	close(s.hubDone)
}

// writeLoop drains the client's queue onto the socket, then closes the conn.
// It is the sole closer of the connection after registration.
func (s *Server) writeLoop(c *client) {
	defer s.wg.Done()
	defer c.conn.Close()
	for line := range c.out {
		if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
			return // conn dead or force-closed; nothing more to do
		}
	}
}

// handleConn is the per-connection reader goroutine (reader-per-connection).
// It performs the name handshake, then forwards message lines to the hub,
// and finally asks the hub to remove the client. It never closes the conn
// itself once registration succeeded — the writer owns that. Idle timeout
// is enforced via SetReadDeadline per line; overlong lines are rejected via
// ReadLine's maxLineBytes check.
func (s *Server) handleConn(conn net.Conn) {
	br := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	line, err := ReadLine(br)
	if err != nil || strings.TrimSpace(line) == "" {
		conn.Close()
		return
	}

	c := &client{conn: conn, out: make(chan string, clientOutBuf)}
	req := &registerReq{name: strings.TrimSpace(line), reply: make(chan string, 1), c: c}
	select {
	case s.regCh <- req:
	case <-s.hubDone:
		conn.Close()
		return
	}
	var assigned string
	select {
	case assigned = <-req.reply:
	case <-s.hubDone:
		conn.Close()
		return
	}

	for {
		_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
		line, err := ReadLine(br)
		if err != nil {
			if line != "" { // final unterminated line still counts as a msg
				select {
				case s.eventCh <- Event{From: assigned, Kind: "msg", Payload: line}:
				case <-s.hubDone:
				}
			}
			break
		}
		if line == "" {
			continue
		}
		select {
		case s.eventCh <- Event{From: assigned, Kind: "msg", Payload: line}:
		case <-s.hubDone:
			return
		}
	}

	// Reader is done (EOF or error); hand cleanup to the hub.
	select {
	case s.unregCh <- c:
	case <-s.hubDone:
	}
}

// Serve listens on addr and blocks until the listener closes or Shutdown is
// called. Each accepted connection spawns a reader goroutine. Concurrent
// connections are capped at maxConns (excess are closed immediately) and the
// handshake is bounded by handshakeTimeout; registered conns use idleTimeout
// per ReadLine (see handleConn).
func (s *Server) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ln.Close()
		return nil // closed before Serve started: treat as clean shutdown
	}
	s.ln = ln
	s.mu.Unlock()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil // Shutdown closed us: a clean stop
			}
			return err
		}
		// Bound concurrent connections: drop excess immediately.
		select {
		case s.sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		go func(c net.Conn) {
			defer func() { <-s.sem }()
			s.handleConn(c)
		}(conn)
	}
}

// Shutdown stops accepting, politely disconnects clients (a goodbye system
// line, then close), and unblocks Serve. If ctx expires (or has already
// expired) before all writers finish draining, remaining connections are
// force-closed and ctx.Err() is returned. A second call returns net.ErrClosed
// wrapped.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("chatwire: %w", net.ErrClosed)
	}
	s.closed = true
	ln := s.ln
	s.mu.Unlock()

	if ln != nil {
		ln.Close() // unblocks Serve with net.ErrClosed → nil return
	}
	close(s.stopCh) // hub says goodbye, closes outs and subscribers, closes hubDone

	if err := ctx.Err(); err != nil {
		// Deterministic fast path for an already-expired context: skip the
		// polite wait entirely instead of racing select between two ready
		// cases, and always force-close.
		s.forceCloseConns()
		return err
	}
	select {
	case <-s.doneCh: // hub finished AND every writer drained
		return nil
	case <-ctx.Done():
		s.forceCloseConns()
		return ctx.Err()
	}
}

// forceCloseConns kills every currently-tracked connection so wedged writers
// unblock and the server can be torn down under a deadline. Because conns
// are tracked incrementally at register/drop/leave time, this is correct
// even if the hub never reached finalize.
func (s *Server) forceCloseConns() {
	s.connsMu.Lock()
	conns := s.liveConns
	s.liveConns = make(map[net.Conn]struct{})
	s.connsMu.Unlock()
	for cn := range conns {
		cn.Close() // closing an already-closed conn is a harmless no-op
	}
}

// Subscribe returns a receive-only channel receiving every event until
// Unsubscribe or server close (which closes the channel). The hub DROPS
// events when the buffer is full — it never blocks. buf is clamped to a
// minimum of 1; values <= 0 behave as capacity 1.
func (s *Server) Subscribe(buf int) <-chan Event {
	if buf < 1 {
		buf = 1
	}
	ch := make(chan Event, buf)
	select {
	case s.subAddCh <- ch:
	case <-s.hubDone:
		close(ch)
	}
	return ch
}

// Unsubscribe removes ch from the hub's subscriber set. The channel is not
// closed here; ownership stays with the caller after removal.
func (s *Server) Unsubscribe(ch <-chan Event) {
	select {
	case s.subDelCh <- ch:
	case <-s.hubDone:
	}
}
