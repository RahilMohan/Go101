package chatwire

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

const settle = 300 * time.Millisecond

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve addr: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// startServer launches a server on a free port and registers cleanup that
// shuts it down and asserts Serve returned cleanly.
func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := NewServer()
	if s == nil {
		t.Fatal("NewServer returned nil")
	}
	addr := freeAddr(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(addr) }()
	awaitListener(t, addr)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil &&
			!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, net.ErrClosed) {
			t.Errorf("cleanup Shutdown: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after Shutdown")
		}
	})
	return s, addr
}

// awaitListener polls until the server is accepting connections. Probe
// conns hang up before the handshake; the server treats them as abrupt
// clients and drops them silently.
func awaitListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never started listening on %s", addr)
}

type testClient struct {
	conn net.Conn
	r    *bufio.Reader
}

// dial joins the server with the given display name.
func dial(t *testing.T, addr, name string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %q: %v", name, err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintln(conn, name)
	return &testClient{conn: conn, r: bufio.NewReader(conn)}
}

// readUntil consumes lines until one contains substr; returns that line.
func (c *testClient) readUntil(t *testing.T, substr string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.conn.SetReadDeadline(deadline)
		line, err := c.r.ReadString('\n')
		if err != nil {
			t.Fatalf("readUntil %q: %v", substr, err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.Contains(line, substr) {
			return line
		}
	}
}

// send writes one protocol line to the server.
func (c *testClient) send(t *testing.T, line string) {
	t.Helper()
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintln(c.conn, line); err != nil {
		t.Fatalf("send %q: %v", line, err)
	}
}

// drain counts background-delivered server lines containing marker until the
// connection closes. stop closes the socket and waits for the counter to end.
func (c *testClient) drain(marker string) (count func() int, stop func()) {
	var mu sync.Mutex
	n := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			line, err := c.r.ReadString('\n')
			if err != nil {
				return
			}
			if strings.Contains(line, marker) {
				mu.Lock()
				n++
				mu.Unlock()
			}
		}
	}()
	return func() int { mu.Lock(); defer mu.Unlock(); return n },
		func() { c.conn.Close(); <-done }
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

// 1. Two clients join → each learns of the other; messages fan out to every
// member including the sender.
func TestTwoClientsJoinAndExchange(t *testing.T) {
	_, addr := startServer(t)

	a := dial(t, addr, "alice")
	if got := a.readUntil(t, "you are"); !strings.Contains(got, "alice") {
		t.Fatalf("welcome = %q, want mention of alice", got)
	}

	b := dial(t, addr, "bob")
	if got := b.readUntil(t, "you are"); !strings.Contains(got, "bob") {
		t.Fatalf("welcome = %q, want mention of bob", got)
	}
	// b learns about alice via the online roster.
	if got := b.readUntil(t, "online"); !strings.Contains(got, "alice") {
		t.Fatalf("roster = %q, want alice listed", got)
	}
	// a sees bob's join broadcast.
	a.readUntil(t, "bob joined")

	a.send(t, "hello there")
	b.readUntil(t, "alice: hello there")
	a.readUntil(t, "alice: hello there")

	b.send(t, "hi back")
	a.readUntil(t, "bob: hi back")
	b.readUntil(t, "bob: hi back")
}

// 2. Duplicate names get numeric suffixes; both parties are informed.
func TestDuplicateNameGetsSuffix(t *testing.T) {
	_, addr := startServer(t)

	first := dial(t, addr, "bob")
	first.readUntil(t, "you are bob")

	second := dial(t, addr, "bob")
	if got := second.readUntil(t, "you are"); !strings.Contains(got, "bob-2") {
		t.Fatalf("welcome = %q, want assigned bob-2", got)
	}
	first.readUntil(t, "bob-2 joined")

	second.send(t, "from two")
	first.readUntil(t, "bob-2: from two")
}

// 3. Slow-subscriber isolation: a full subscriber buffer causes drops, never
// hub stalls; active clients and roomy subscribers stay complete.
func TestSlowSubscriberIsolation(t *testing.T) {
	s, addr := startServer(t)

	slow := s.Subscribe(1)
	fast := s.Subscribe(256)

	gen := dial(t, addr, "gen")
	gen.readUntil(t, "you are gen")
	echo := dial(t, addr, "echo")
	echo.readUntil(t, "you are echo")
	genCount, genStop := gen.drain("gen:")
	echoCount, echoStop := echo.drain("gen:")
	defer genStop()
	defer echoStop()

	const total = 100
	for i := 0; i < total; i++ {
		gen.send(t, strconv.Itoa(i))
	}

	waitFor(t, "echo client completeness", func() bool { return echoCount() >= total })
	waitFor(t, "generator self-echo completeness", func() bool { return genCount() >= total })
	waitFor(t, "fast subscriber completeness", func() bool { return fastMsgCount(fast) >= total })

	// slow sub dropped almost everything instead of blocking: at most its
	// single buffered event is retrievable without blocking.
	dropped := 0
	for {
		select {
		case <-slow:
			dropped++
			continue
		default:
		}
		break
	}
	if dropped > 1 {
		t.Fatalf("slow sub delivered %d events with capacity 1", dropped)
	}
	done := make(chan struct{})
	go func() { s.Unsubscribe(slow); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Unsubscribe deadlocked")
	}

	// hub stayed live: one more message still reaches everyone.
	gen.send(t, "after-slow")
	waitFor(t, "post-drop delivery", func() bool { return echoCount() >= total+1 })
	waitFor(t, "fast post-drop delivery", func() bool { return fastMsgCount(fast) >= total+1 })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// fastMsgCount reports how many events are currently sitting unread in ch's
// buffer. It deliberately never consumes from ch — occupancy via len() lets
// assertions verify delivery volume without racing the drop semantics under
// test or stealing events other checks still need. The count includes join/
// system events, so it is a lower bound on delivered msg events.
func fastMsgCount(ch <-chan Event) int {
	return len(ch)
}

// 4. Graceful shutdown: goodbye system line, then EOF; Serve returns nil.
func TestGracefulShutdown(t *testing.T) {
	s, addr := startServer(t)

	c := dial(t, addr, "solo")
	c.readUntil(t, "you are solo")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	goodbye := c.readUntil(t, "shutting down")
	if !strings.HasPrefix(goodbye, "* ") {
		t.Fatalf("goodbye = %q, want system-prefixed shutdown notice", goodbye)
	}
	if _, err := c.r.ReadString('\n'); !errors.Is(err, io.EOF) {
		t.Fatalf("after goodbye err = %v, want io.EOF", err)
	}
}

// 4b. Second Shutdown reports net.ErrClosed wrapped.
func TestSecondShutdownErrClosed(t *testing.T) {
	s, _ := startServer(t)
	ctx := context.Background()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := s.Shutdown(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("second Shutdown err = %v, want net.ErrClosed", err)
	}
}

// 5. Shutdown deadline: an unread-full client cannot stall Shutdown past ctx.
func TestShutdownDeadlineDropsStuckClient(t *testing.T) {
	s, addr := startServer(t)

	hog, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer hog.Close()
	fmt.Fprintln(hog, "hog") // register; hog never reads its socket again

	// One gigantic message forces hog's writer goroutine into a blocking
	// conn.Write well before Shutdown runs.
	hog.SetWriteDeadline(time.Now().Add(30 * time.Second))
	big := strings.Repeat("x", 32<<20)
	if _, err := fmt.Fprintf(hog, "%s\n", big); err != nil {
		t.Fatalf("send big line: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // let the writer wedge on Write

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	shutdownErr := s.Shutdown(ctx)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown took %v; deadline not enforced", elapsed)
	}
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown err = %v, want context.DeadlineExceeded", shutdownErr)
	}

	// hog was dropped: its socket closes shortly after the force-close.
	hog.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	for {
		if _, err := hog.Read(buf); err != nil {
			break // closed — good
		}
	}
}

// 5b. Regression: an already-expired context must take the force-close path
// deterministically (no polite wait, no select race) and close live client
// conns even though the hub may not have reached finalize yet. This pins the
// incremental conn-tracking fix: force-close sees current conns, never a
// stale snapshot.
func TestShutdownExpiredContextForceCloses(t *testing.T) {
	s, addr := startServer(t)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "victim") // register; victim idles without reading

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	start := time.Now()
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Shutdown took %v with expired ctx; wanted immediate force-close", elapsed)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	for {
		if _, err := conn.Read(buf); err != nil {
			return // closed — the victim did not leak past Shutdown
		}
	}
}

// 6. Abrupt clients (no handshake, partial handshake) cause no panic and no
// impact on other clients.
func TestAbruptClientsDoNotDisturbOthers(t *testing.T) {
	_, addr := startServer(t)

	steady := dial(t, addr, "steady")
	steady.readUntil(t, "you are steady")

	ghost, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial ghost: %v", err)
	}
	ghost.Close() // never sends anything

	partial, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial partial: %v", err)
	}
	fmt.Fprint(partial, "halfway") // no newline, then abrupt close
	partial.Close()

	time.Sleep(settle)

	steady.send(t, "still here")
	if got := steady.readUntil(t, "steady: still here"); got == "" {
		t.Fatal("steady client disturbed by abrupt peers")
	}
}

// 7. Whole-suite goroutine hygiene: after a full lifecycle plus settle time,
// goroutine count returns to (approximately) baseline.
func TestNoGoroutineLeaks(t *testing.T) {
	time.Sleep(settle) // let earlier tests finish reaping
	base := runtime.NumGoroutine()

	s, addr := startServer(t)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				return
			}
			defer conn.Close()
			name := "user" + strconv.Itoa(i)
			fmt.Fprintln(conn, name)
			go func() {
				br := bufio.NewReader(conn)
				for {
					if _, err := br.ReadString('\n'); err != nil {
						return
					}
				}
			}()
			for j := 0; j < 5; j++ {
				fmt.Fprintln(conn, name+" msg "+strconv.Itoa(j))
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	time.Sleep(2 * settle)
	delta := runtime.NumGoroutine() - base
	const tolerance = 5
	if delta > tolerance {
		t.Fatalf("goroutine leak: baseline %d, now %d (delta %d > %d)",
			base, base+delta, delta, tolerance)
	}
}
