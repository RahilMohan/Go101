package minikv

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// op is one command to apply against an engine.
type op struct {
	cmd  string
	args []string
}

// mixedOps returns a deterministic sequence of n mixed SET / SET EX /
// DEL commands over a shared keyspace. Values include whitespace and a
// CRLF pair in the first op to prove binary-safe framing end to end.
func mixedOps(n int) []op {
	rng := rand.New(rand.NewSource(42))
	keys := make([]string, 20)
	for i := range keys {
		keys[i] = fmt.Sprintf("key%d", i)
	}
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		k := keys[rng.Intn(len(keys))]
		switch rng.Intn(3) {
		case 0:
			v := fmt.Sprintf("val-%d", i)
			if i == 0 {
				v = "line1\r\nline2 with  spaces"
			}
			ops = append(ops, op{"SET", []string{k, v}})
		case 1:
			ops = append(ops, op{"SET", []string{k, fmt.Sprintf("exp-%d", i), "EX", "3600"}})
		default:
			ops = append(ops, op{"DEL", []string{keys[rng.Intn(len(keys))]}})
		}
	}
	return ops
}

// snapshot captures the full observable state of an engine: every key
// it reports plus its value.
func snapshot(t *testing.T, e *Engine) map[string]string {
	t.Helper()
	m := make(map[string]string)
	for _, k := range e.Keys() {
		v, ok := e.Get(k)
		if !ok {
			t.Fatalf("Keys reported %q but Get missed it", k)
		}
		m[k] = v
	}
	return m
}

func mustApply(t *testing.T, e *Engine, o op) string {
	t.Helper()
	out, err := e.Apply(o.cmd, o.args...)
	if err != nil {
		t.Fatalf("Apply %s %v: %v", o.cmd, o.args, err)
	}
	return out
}

// readRespFrame reads exactly one RESP reply frame from r and returns
// its raw bytes (including terminators), following bulk-string payloads.
func readRespFrame(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read response line: %v", err)
	}
	if strings.HasPrefix(line, "$") && strings.TrimSpace(line) != "$-1" {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "$")))
		if err != nil || n < 0 {
			t.Fatalf("bad bulk length line %q", line)
		}
		body := make([]byte, n+2) // payload + CRLF
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read bulk body: %v", err)
		}
		return line + string(body)
	}
	return line
}

func sendAndExpect(t *testing.T, conn net.Conn, r *bufio.Reader, wire, want string) {
	t.Helper()
	if _, err := conn.Write([]byte(wire)); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if got := readRespFrame(t, r); got != want {
		t.Fatalf("wire reply mismatch:\n sent  %q\n want  %q\n got   %q", wire, want, got)
	}
}

// respBulk encodes args as a RESP array of bulk strings.
func respBulk(args ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	return b.String()
}

// dataLen reports the number of live entries by inspecting engine
// internals directly — deliberately bypassing lazy expiry so tests can
// observe active-sweep deletions without triggering them via reads.
func dataLen(e *Engine) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.data)
}

// waitFor polls cond until true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, what)
}

// ---------------------------------------------------------------------------
// 1. protocol round-trip over real TCP
// ---------------------------------------------------------------------------

func TestProtocolRoundTrip(t *testing.T) {
	srv, err := NewServer(Config{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown(context.Background())
	go srv.ListenAndServe()

	conn, err := net.Dial("tcp", srv.ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)

	sendAndExpect(t, conn, r, respBulk("PING"), "+PONG\r\n")
	sendAndExpect(t, conn, r, respBulk("SET", "foo", "bar"), "+OK\r\n")
	sendAndExpect(t, conn, r, respBulk("SET", "foo", "bar2"), "+OK\r\n")
	sendAndExpect(t, conn, r, respBulk("GET", "foo"), "$4\r\nbar2\r\n")
	sendAndExpect(t, conn, r, respBulk("GET", "missing"), "$-1\r\n")
	sendAndExpect(t, conn, r, respBulk("SET", "a", "1"), "+OK\r\n")
	sendAndExpect(t, conn, r, respBulk("SET", "b", "2"), "+OK\r\n")
	sendAndExpect(t, conn, r, respBulk("KEYS"), "$7\r\na\nb\nfoo\r\n") // sorted: a, b, foo
	sendAndExpect(t, conn, r, respBulk("DEL", "foo"), ":1\r\n")
	sendAndExpect(t, conn, r, respBulk("DEL", "foo"), ":0\r\n")
	sendAndExpect(t, conn, r, respBulk("KEYS"), "$3\r\na\nb\r\n")
	sendAndExpect(t, conn, r, respBulk("FROB"), "-ERR unknown command 'FROB'\r\n")
	sendAndExpect(t, conn, r, respBulk("SET", "onlykey"), "-ERR wrong number of arguments for 'SET'\r\n")

	// TTL over the wire: EX 60 then TTL reports whole seconds remaining.
	sendAndExpect(t, conn, r, respBulk("SET", "tk", "tv", "EX", "60"), "+OK\r\n")
	sendAndExpect(t, conn, r, respBulk("TTL", "tk"), expectTTLOf(t, srv.eng))
	sendAndExpect(t, conn, r, respBulk("SET", "pk", "pv"), "+OK\r\n")
	sendAndExpect(t, conn, r, respBulk("TTL", "pk"), ":-1\r\n")
	sendAndExpect(t, conn, r, respBulk("TTL", "nope-key"), ":-2\r\n")
}

// expectTTLOf reads the engine-side TTL for tk and formats the exact
// integer frame the server must have replied with (bounded 57..60).
func expectTTLOf(t *testing.T, e *Engine) string {
	t.Helper()
	d, ok := e.TTL("tk")
	if !ok || d < 55*time.Second || d > 61*time.Second {
		t.Fatalf("TTL(tk) = (%v, %v); want ~60s", d, ok)
	}
	return ":" + strconv.FormatInt(ceilSeconds(d), 10) + "\r\n"
}

// ---------------------------------------------------------------------------
// 2. WAL replay: state identical after close + reopen
// ---------------------------------------------------------------------------

func TestWALReplayIdenticalState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.bin")
	a, err := NewEngine(Config{WALPath: path, SyncEveryN: 1})
	if err != nil {
		t.Fatalf("NewEngine A: %v", err)
	}
	ops := mixedOps(100)
	for _, o := range ops {
		mustApply(t, a, o)
	}
	want := snapshot(t, a)
	wantKeys := a.Keys()
	if err := a.Close(); err != nil {
		t.Fatalf("close A: %v", err)
	}

	b, err := NewEngine(Config{WALPath: path})
	if err != nil {
		t.Fatalf("NewEngine B (replay): %v", err)
	}
	defer b.Close()
	if got := b.Keys(); !equalStrings(got, wantKeys) {
		t.Fatalf("replayed keys mismatch:\n want %v\n got  %v", wantKeys, got)
	}
	got := snapshot(t, b)
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("key %q lost across replay", k)
		}
		if gv != wv {
			t.Fatalf("key %q = %q after replay, want %q", k, gv, wv)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("replayed state has %d keys, want %d", len(got), len(want))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 3. crash consistency: copy WAL mid-run, reopen without clean shutdown
// ---------------------------------------------------------------------------

func TestCrashConsistency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.bin")

	const n = 50
	a, err := NewEngine(Config{WALPath: path}) // SyncEveryN=0: OS-batched writes
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for i := 0; i < n; i++ {
		mustApply(t, a, op{"SET", []string{fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)}})
	}

	// Simulate a crash: grab the WAL bytes while the handle is still
	// open, then abandon the handle without fsync/cleanup.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WAL mid-run: %v", err)
	}
	a.kill()

	crashCopy := filepath.Join(dir, "crash-copy.bin")
	if err := os.WriteFile(crashCopy, raw, 0o644); err != nil {
		t.Fatalf("write crash copy: %v", err)
	}
	b, err := NewEngine(Config{WALPath: crashCopy})
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer b.Close()
	for i := 0; i < n; i++ {
		k, wv := fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)
		if v, ok := b.Get(k); !ok || v != wv {
			t.Fatalf("after crash: Get(%q) = (%q, %v), want (%q, true)", k, v, ok, wv)
		}
	}
	if got := len(b.Keys()); got != n {
		t.Fatalf("after crash: %d keys, want %d", got, n)
	}

	// A torn final record (last write cut off mid-record) is dropped
	// silently; all earlier records survive.
	torn := filepath.Join(dir, "torn-crash.bin")
	if err := os.WriteFile(torn, raw[:len(raw)-3], 0o644); err != nil {
		t.Fatalf("write torn copy: %v", err)
	}
	c, err := NewEngine(Config{WALPath: torn})
	if err != nil {
		t.Fatalf("reopen torn WAL: %v", err)
	}
	defer c.Close()
	for i := 0; i < n-1; i++ {
		k, wv := fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)
		if v, ok := c.Get(k); !ok || v != wv {
			t.Fatalf("torn replay: Get(%q) = (%q, %v), want present", k, v, ok)
		}
	}
	if _, ok := c.Get(fmt.Sprintf("k%d", n-1)); ok {
		t.Fatal("torn replay kept the incomplete final record")
	}
}

// ---------------------------------------------------------------------------
// 4. torn-tail fuzz-ish: every truncation point replays cleanly and
// keeps exactly the complete-prefix records; mid-file corruption errors
// ---------------------------------------------------------------------------

func TestTornTailTruncations(t *testing.T) {
	dir := t.TempDir()

	// Build a valid WAL while recording exact byte offsets of each
	// record boundary (file size after each Apply).
	const m = 10
	keys := make([]string, m)
	for i := range keys {
		keys[i] = fmt.Sprintf("tz%d", i)
	}
	buildOps := make([]op, m)
	for i := range buildOps {
		buildOps[i] = op{"SET", []string{keys[i], fmt.Sprintf("%d bytes", i)}}
	}

	boundsPath := filepath.Join(dir, "bounds.bin")
	be, err := NewEngine(Config{WALPath: boundsPath})
	if err != nil {
		t.Fatalf("NewEngine bounds: %v", err)
	}
	bounds := []int64{0}
	for _, o := range buildOps {
		mustApply(t, be, o)
		st, err := os.Stat(boundsPath)
		if err != nil {
			t.Fatalf("stat bounds WAL: %v", err)
		}
		bounds = append(bounds, st.Size())
	}
	be.kill()

	validPath := filepath.Join(dir, "valid.bin")
	os.WriteFile(validPath, func() []byte {
		raw, err := os.ReadFile(boundsPath)
		if err != nil {
			t.Fatalf("read valid WAL: %v", err)
		}
		return raw
	}(), 0o644)
	raw, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatalf("read raw WAL: %v", err)
	}

	// Truncate at every record boundary and at boundary-1 (mid-header).
	points := map[int64]bool{}
	for _, p := range bounds {
		points[p] = true
		if p > 0 {
			points[p-1] = true
		}
	}
	for p := range points {
		p := p
		t.Run(fmt.Sprintf("trunc-%d", p), func(t *testing.T) {
			tp := filepath.Join(dir, fmt.Sprintf("trunc-%d.bin", p))
			if err := os.WriteFile(tp, raw[:p], 0o644); err != nil {
				t.Fatalf("write truncation: %v", err)
			}
			e, err := NewEngine(Config{WALPath: tp})
			if err != nil {
				t.Fatalf("replay truncated WAL at %d: %v", p, err)
			}
			defer e.Close()
			var wantKeys []string
			wantVals := map[string]string{}
			for j, o := range buildOps {
				if bounds[j+1] <= p { // record fully inside the file
					wantKeys = append(wantKeys, o.args[0])
					wantVals[o.args[0]] = o.args[1]
				}
			}
			gotKeys := e.Keys()
			if !equalStrings(gotKeys, wantKeys) {
				t.Fatalf("keys after truncation at %d:\n want %v\n got  %v", p, wantKeys, gotKeys)
			}
			for k, wv := range wantVals {
				if v, ok := e.Get(k); !ok || v != wv {
					t.Fatalf("Get(%q) = (%q, %v), want %q", k, v, ok, wv)
				}
			}
		})
	}
}

func TestMidFileCorruptionIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.bin")
	e, err := NewEngine(Config{WALPath: path})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for i := 0; i < 5; i++ {
		mustApply(t, e, op{"SET", []string{fmt.Sprintf("c%d", i), "x"}})
	}
	e.kill()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read WAL: %v", err)
	}
	idx := bytes.Index(raw, []byte("SET"))
	if idx < 0 {
		t.Fatal("no SET found in WAL to corrupt")
	}
	corrupt := append([]byte(nil), raw...)
	corrupt[idx+1] = 'X' // first command name becomes "SXT" mid-file
	cp := filepath.Join(dir, "corrupt.bin")
	if err := os.WriteFile(cp, corrupt, 0o644); err != nil {
		t.Fatalf("write corrupt copy: %v", err)
	}
	if _, err := NewEngine(Config{WALPath: cp}); err == nil {
		t.Fatal("replay of mid-file corruption returned nil error, want error")
	} else if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("error %q should mention corruption", err)
	}
}

// ---------------------------------------------------------------------------
// 5. TTL: lazy expiry, active sweep, TTL reporting
// ---------------------------------------------------------------------------

func TestTTLLazyExpiry(t *testing.T) {
	e, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()
	mustApply(t, e, op{"SET", []string{"lk", "v", "EX", "1"}})
	if d, ok := e.TTL("lk"); !ok || d <= 0 || d > time.Second {
		t.Fatalf("TTL(lk) = (%v, %v), want (0,1s]", d, ok)
	}
	time.Sleep(1100 * time.Millisecond)
	if v, ok := e.Get("lk"); ok {
		t.Fatalf("lazy expiry failed: Get(lk) = (%q, true)", v)
	}
	for _, k := range e.Keys() {
		if k == "lk" {
			t.Fatal("expired key still listed by Keys")
		}
	}
}

func TestTTLActiveSweepRemovesWithoutReads(t *testing.T) {
	e, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()
	mustApply(t, e, op{"SET", []string{"ak", "v", "EX", "1"}})
	// No reads of "ak" from here on: only the sweeper can remove it.
	waitFor(t, 4*time.Second, "active sweep to remove expired key", func() bool {
		return dataLen(e) == 0
	})
}

func TestTTLReporting(t *testing.T) {
	e, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()
	mustApply(t, e, op{"SET", []string{"tk", "v", "EX", "30"}})
	d, ok := e.TTL("tk")
	if !ok || d < 28*time.Second || d > 31*time.Second {
		t.Fatalf("TTL(tk) = (%v, %v), want ~30s", d, ok)
	}
	out := mustApply(t, e, op{"TTL", []string{"tk"}})
	if n, _ := strconv.Atoi(out); n < 28 || n > 30 {
		t.Fatalf("Apply(TTL tk) = %q, want seconds in [28,30]", out)
	}
	mustApply(t, e, op{"SET", []string{"pk", "v"}})
	if d, ok := e.TTL("pk"); ok {
		t.Fatalf("TTL(pk) = (%v, true), want no expiry", d)
	}
	if out := mustApply(t, e, op{"TTL", []string{"pk"}}); out != "-1" {
		t.Fatalf("TTL without expiry = %q, want -1", out)
	}
	if out := mustApply(t, e, op{"TTL", []string{"missing"}}); out != "-2" {
		t.Fatalf("TTL missing key = %q, want -2", out)
	}
	// SET overwrites clear the previous expiry unless EX is given again.
	mustApply(t, e, op{"SET", []string{"tk", "v2"}})
	if _, ok := e.TTL("tk"); ok {
		t.Fatal("plain SET did not clear prior expiry")
	}
}

// ---------------------------------------------------------------------------
// 6. LRU eviction
// ---------------------------------------------------------------------------

func TestLRUEvictionOrder(t *testing.T) {
	memPerEntry := entryOverhead + 4 // key "kN" (2) + value "vN" (2)
	cfg := Config{
		MaxMemory:      memPerEntry * 3,
		EvictionPolicy: "lru",
	}
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	for i := 1; i <= 3; i++ {
		mustApply(t, e, op{"SET", []string{fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)}})
	}
	// Touch k1 so k2 becomes the least-recently accessed.
	if _, ok := e.Get("k1"); !ok {
		t.Fatal("Get(k1) missed before eviction test")
	}
	// present inspects membership via Keys(), which deliberately does
	// NOT count as an access — a Get() here would refresh recency and
	// change who the next victim should be.
	present := func() map[string]bool {
		m := make(map[string]bool)
		for _, k := range e.Keys() {
			m[k] = true
		}
		return m
	}

	mustApply(t, e, op{"SET", []string{"k4", "v4"}}) // exceeds cap → evict oldest
	p := present()
	if p["k2"] {
		t.Fatal("expected LRU victim k2 evicted, still present")
	}
	for _, k := range []string{"k1", "k3", "k4"} {
		if !p[k] {
			t.Fatalf("non-victim %q was evicted", k)
		}
	}
	// Another insert evicts the next-oldest survivor, k3.
	mustApply(t, e, op{"SET", []string{"k5", "v5"}})
	p = present()
	if p["k3"] {
		t.Fatal("expected second victim k3 evicted")
	}
	if !p["k1"] || !p["k4"] || !p["k5"] {
		t.Fatalf("unexpected survivors after second eviction: %v", p)
	}
}

func TestNoEvictionWhenPolicyNone(t *testing.T) {
	cfg := Config{MaxMemory: entryOverhead + 2} // absurdly small on purpose
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()
	for i := 0; i < 5; i++ {
		mustApply(t, e, op{"SET", []string{fmt.Sprintf("n%d", i), "x"}})
	}
	if got := len(e.Keys()); got != 5 {
		t.Fatalf("policy none evicted entries: %d keys remain, want 5", got)
	}
}

// ---------------------------------------------------------------------------
// 7. concurrency under -race
// ---------------------------------------------------------------------------

func TestConcurrentApply(t *testing.T) {
	e, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				k := fmt.Sprintf("k%d", (g*i)%32)
				var err error
				switch i % 3 {
				case 0:
					_, err = e.Apply("SET", k, fmt.Sprintf("g%d-i%d", g, i))
				case 1:
					_, err = e.Apply("GET", k)
				default:
					_, err = e.Apply("DEL", k)
				}
				if err != nil {
					t.Errorf("goroutine %d: Apply: %v", g, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if _, err := e.Apply("SET", "final", "ok"); err != nil {
		t.Fatalf("post-race SET: %v", err)
	}
	if v, ok := e.Get("final"); !ok || v != "ok" {
		t.Fatalf("engine unusable after concurrent load: (%q, %v)", v, ok)
	}
}

// ---------------------------------------------------------------------------
// 8. SyncEveryN modes both functional
// ---------------------------------------------------------------------------

func TestSyncEveryNModes(t *testing.T) {
	for _, n := range []int{0, 1, 5} {
		n := n
		t.Run(fmt.Sprintf("sync-every-%d", n), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), fmt.Sprintf("wal-%d.bin", n))
			a, err := NewEngine(Config{WALPath: path, SyncEveryN: n})
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			for i := 0; i < 25; i++ {
				mustApply(t, a, op{"SET", []string{fmt.Sprintf("s%d", i), fmt.Sprintf("v%d", i)}})
			}
			if err := a.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			b, err := NewEngine(Config{WALPath: path})
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer b.Close()
			for i := 0; i < 25; i++ {
				k, wv := fmt.Sprintf("s%d", i), fmt.Sprintf("v%d", i)
				if v, ok := b.Get(k); !ok || v != wv {
					t.Fatalf("SyncEveryN=%d: Get(%q) = (%q, %v), want %q", n, k, v, ok, wv)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// config validation & server lifecycle
// ---------------------------------------------------------------------------

func TestConfigValidation(t *testing.T) {
	if _, err := NewEngine(Config{EvictionPolicy: "fifo"}); err == nil {
		t.Fatal("unknown eviction policy accepted")
	}
	if _, err := NewEngine(Config{SyncEveryN: -1}); err == nil {
		t.Fatal("negative SyncEveryN accepted")
	}
}

func TestServerLifecycle(t *testing.T) {
	srv, err := NewServer(Config{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	conn, err := net.Dial("tcp", srv.ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	r := bufio.NewReader(conn)
	sendAndExpect(t, conn, r, respBulk("PING"), "+PONG\r\n")
	conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ListenAndServe returned %v after Shutdown, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not return after Shutdown")
	}
}
