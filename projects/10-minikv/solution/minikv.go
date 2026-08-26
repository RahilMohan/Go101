// Package minikv is a small Redis-like key/value store: a RESP-subset
// binary protocol served over TCP, a write-ahead log with configurable
// fsync batching, lazy + active TTL expiry, and LRU eviction under a
// memory cap. It teaches binary wire protocols, length-prefixed
// append-only logs, crash-consistency testing against torn tail
// writes, fsync durability tradeoffs, TTL sweep loops with bounded
// work, and approximate LRU eviction.
//
// # Crash model
//
// The WAL is a sequence of length-prefixed records (uint32 big-endian
// length + payload). A payload encodes one command as a count followed
// by length-prefixed argument bytes, so records are binary-safe. On
// open the log is replayed from the start; an incomplete record at the
// very end of the file — a torn tail left by a crash mid-write — is
// dropped with a warning to stderr, while corruption inside a complete
// record is a hard error. This mirrors how real append-only logs
// distinguish "the last write never finished" from "the disk rotted".
//
// # Durability tradeoff
//
// Every record is handed to the OS with write(2) immediately, so a
// process crash loses nothing that Apply returned for. Only a machine
// crash can lose data, and SyncEveryN controls that window:
// SyncEveryN=1 fsyncs after every write (durable, slow), SyncEveryN>1
// batches (fsync every N writes), SyncEveryN=0 never fsyncs explicitly
// (the OS flushes on its own schedule). This is the classic group-
// commit knob; the tests exercise all three modes.
package minikv

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// sweepInterval is how often the active expiry sweeper wakes up.
	sweepInterval = time.Second
	// maxSweepScan bounds the work per sweep tick: at most this many
	// entries are examined per pass, so a huge keyspace cannot turn
	// one tick into a stop-the-world scan (Redis does the same with
	// sampled sweeps).
	maxSweepScan = 128
	// maxRecordLen rejects absurd WAL record lengths so a corrupted
	// length prefix cannot trigger a giant allocation.
	maxRecordLen = 1 << 26 // 64 MiB
	// entryOverhead approximates per-key memory beyond key+value bytes
	// (map slot, string headers, timestamps).
	entryOverhead int64 = 48
)

// Config configures an Engine or Server.
type Config struct {
	Addr           string // TCP listen address (Server only)
	WALPath        string // empty = in-memory only (no persistence)
	SyncEveryN     int    // fsync every N WAL writes; 1=durability per write, 0=batched by the OS
	MaxMemory      int64  // bytes; 0 = unlimited; when exceeded, evict until under
	EvictionPolicy string // "none" (default) | "lru"
}

type entry struct {
	value      string
	expiresAt  time.Time // zero = no expiry
	lastAccess time.Time
}

func memOf(key, value string) int64 { return entryOverhead + int64(len(key)) + int64(len(value)) }

// Engine is the testable core: a mutex-guarded map of entries with TTL
// metadata, a WAL appender, and a bounded active-expiry sweeper. All
// exported methods are safe for concurrent use.
type Engine struct {
	mu        sync.Mutex
	data      map[string]entry
	memUsed   int64
	maxMemory int64
	policy    string
	wal       *walFile
	stop      chan struct{}
	sweepDone chan struct{}
	closeOnce sync.Once
}

// walFile appends length-prefixed records to an on-disk log. It owns
// the fsync batching counter; every append still reaches the OS via
// write(2), so only machine crashes can lose acknowledged writes when
// syncEvery is 0 or between batch boundaries.
type walFile struct {
	f         *os.File
	path      string
	syncEvery int
	pending   int // writes since last fsync
}

// NewEngine creates an engine, opening (and replaying) the WAL when
// cfg.WALPath is set, and starts the active TTL sweeper.
func NewEngine(cfg Config) (*Engine, error) {
	policy := cfg.EvictionPolicy
	if policy == "" {
		policy = "none"
	}
	switch policy {
	case "none", "lru":
	default:
		return nil, fmt.Errorf("minikv: unknown eviction policy %q (want none|lru)", cfg.EvictionPolicy)
	}
	if cfg.SyncEveryN < 0 {
		return nil, fmt.Errorf("minikv: invalid SyncEveryN %d", cfg.SyncEveryN)
	}

	e := &Engine{
		data:      make(map[string]entry),
		maxMemory: cfg.MaxMemory,
		policy:    policy,
		stop:      make(chan struct{}),
		sweepDone: make(chan struct{}),
	}
	if cfg.WALPath != "" {
		f, err := os.OpenFile(cfg.WALPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("minikv: open WAL %s: %w", cfg.WALPath, err)
		}
		e.wal = &walFile{f: f, path: cfg.WALPath, syncEvery: cfg.SyncEveryN}
		if err := e.replay(); err != nil {
			f.Close()
			return nil, err
		}
		// Replay can resurrect LRU-evicted keys (eviction deletes in
		// memory only; no DEL is logged), pushing memUsed above
		// MaxMemory. Eviction normally runs inside setLocked, so
		// without this pass the cap would be silently violated until
		// the next write. Re-enforce it once, now. Note the eviction
		// itself is still not logged: every future open re-runs this
		// pass, so the invariant holds across restarts.
		e.mu.Lock()
		e.evictIfNeededLocked()
		e.mu.Unlock()
	}
	go e.sweepLoop()
	return e, nil
}

// Apply executes one command (PING, SET, GET, DEL, KEYS, TTL) and
// appends it to the WAL when the engine is persistent. Replies are the
// plain-text payload of the RESP reply: "PONG"/"OK" for PING/SET, the
// value for GET ("" = nil bulk), "0"/"1" for DEL, newline-joined keys
// for KEYS, and "-2"/"-1"/seconds for TTL.
func (e *Engine) Apply(cmd string, args ...string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exec(strings.ToUpper(cmd), args, true)
}

// exec runs one command against the in-memory state. persist=false is
// used during WAL replay to avoid re-applying what we just read.
func (e *Engine) exec(cmd string, args []string, persist bool) (string, error) {
	switch cmd {
	case "PING":
		if len(args) != 0 {
			return "", errArity("PING")
		}
		return "PONG", nil

	case "SET":
		if len(args) != 2 && len(args) != 4 {
			return "", errArity("SET")
		}
		var exp time.Time
		if len(args) == 4 {
			switch {
			case strings.EqualFold(args[2], "EX"):
				secs, err := strconv.Atoi(args[3])
				if err != nil || secs <= 0 {
					return "", errors.New("ERR invalid EX value")
				}
				exp = time.Now().Add(time.Duration(secs) * time.Second)
			case strings.EqualFold(args[2], "EXAT"):
				// Internal form: absolute expiry unix-nano. Only ever
				// produced by WAL replay so TTL clocks survive restarts;
				// not part of the documented wire protocol.
				nanos, err := strconv.ParseInt(args[3], 10, 64)
				if err != nil || nanos <= 0 {
					return "", errors.New("ERR invalid EXAT value")
				}
				exp = time.Unix(0, nanos)
			default:
				return "", errors.New("ERR syntax error")
			}
		}
		e.setLocked(args[0], args[1], exp)
		if persist {
			// Log the absolute expiry, never relative seconds: replay
			// must restore the exact deadline, not restart the clock.
			recArgs := append([]string{cmd}, args...)
			if len(args) == 4 && !exp.IsZero() {
				recArgs = []string{"SET", args[0], args[1], "EXAT", strconv.FormatInt(exp.UnixNano(), 10)}
			}
			if err := e.logWrite(recArgs); err != nil {
				// Note (no rollback): state was already mutated above;
				// if the append fails the in-memory effect outlives the
				// log entry, so a later replay would not see this write.
				// A production WAL would log-then-apply or undo on
				// failure; kept simple here for teaching scope.
				return "", err
			}
		}
		return "OK", nil

	case "GET":
		if len(args) != 1 {
			return "", errArity("GET")
		}
		v, ok := e.getLocked(args[0])
		if !ok {
			return "", nil
		}
		return v, nil

	case "DEL":
		if len(args) != 1 {
			return "", errArity("DEL")
		}
		n := "0"
		if _, ok := e.liveLocked(args[0]); ok {
			e.deleteLocked(args[0])
			n = "1"
		}
		if persist && n == "1" {
			if err := e.logWrite([]string{cmd, args[0]}); err != nil {
				return "", err
			}
		}
		return n, nil

	case "KEYS":
		if len(args) != 0 {
			return "", errArity("KEYS")
		}
		return strings.Join(e.keysLocked(), "\n"), nil

	case "TTL":
		if len(args) != 1 {
			return "", errArity("TTL")
		}
		ent, ok := e.liveLocked(args[0])
		if !ok {
			return "-2", nil
		}
		if ent.expiresAt.IsZero() {
			return "-1", nil
		}
		rem := time.Until(ent.expiresAt)
		if rem <= 0 {
			return "-2", nil
		}
		return strconv.FormatInt(ceilSeconds(rem), 10), nil

	default:
		return "", fmt.Errorf("ERR unknown command '%s'", cmd)
	}
}

func errArity(cmd string) error {
	return fmt.Errorf("ERR wrong number of arguments for '%s'", cmd)
}

func ceilSeconds(d time.Duration) int64 {
	s := d / time.Second
	if d%time.Second != 0 {
		s++
	}
	return int64(s)
}

// --- state mutations (all callers hold e.mu) ---

func (e *Engine) setLocked(key, value string, expiresAt time.Time) {
	now := time.Now()
	if old, ok := e.data[key]; ok {
		e.memUsed -= memOf(key, old.value)
	}
	e.data[key] = entry{value: value, expiresAt: expiresAt, lastAccess: now}
	e.memUsed += memOf(key, value)
	e.evictIfNeededLocked()
}

func (e *Engine) deleteLocked(key string) {
	if ent, ok := e.data[key]; ok {
		e.memUsed -= memOf(key, ent.value)
		delete(e.data, key)
	}
}

// liveLocked returns the entry if present and unexpired, applying lazy
// expiry (and recording the access for LRU purposes) as a side effect
// of any read path.
func (e *Engine) liveLocked(key string) (entry, bool) {
	ent, ok := e.data[key]
	if !ok {
		return entry{}, false
	}
	if !ent.expiresAt.IsZero() && !time.Now().Before(ent.expiresAt) {
		e.deleteLocked(key)
		return entry{}, false
	}
	ent.lastAccess = time.Now()
	e.data[key] = ent
	return ent, true
}

func (e *Engine) getLocked(key string) (string, bool) {
	ent, ok := e.liveLocked(key)
	if !ok {
		return "", false
	}
	return ent.value, true
}

func (e *Engine) keysLocked() []string {
	keys := make([]string, 0, len(e.data))
	now := time.Now()
	for k, ent := range e.data {
		if !ent.expiresAt.IsZero() && !now.Before(ent.expiresAt) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// evictIfNeededLocked enforces MaxMemory by deleting least-recently-
// accessed entries until memory is back under the cap or nothing is
// evictable. With policy "none" (the default) the cap is advisory
// only: nothing is ever evicted.
func (e *Engine) evictIfNeededLocked() {
	if e.maxMemory <= 0 || e.policy != "lru" {
		return
	}
	for e.memUsed > e.maxMemory && len(e.data) > 0 {
		victim := ""
		var oldest time.Time
		for k, ent := range e.data {
			if victim == "" || ent.lastAccess.Before(oldest) {
				victim, oldest = k, ent.lastAccess
			}
		}
		e.deleteLocked(victim)
	}
}

// --- exported read API ---

// Get returns the live value for key, applying lazy expiry.
func (e *Engine) Get(key string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.getLocked(key)
}

// Keys returns the sorted list of live keys.
func (e *Engine) Keys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.keysLocked()
}

// TTL returns the time remaining before key expires; false if the key
// is missing or has no expiry.
func (e *Engine) TTL(key string) (time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ent, ok := e.liveLocked(key)
	if !ok || ent.expiresAt.IsZero() {
		return 0, false
	}
	rem := time.Until(ent.expiresAt)
	if rem <= 0 {
		return 0, false
	}
	return rem, true
}

// --- WAL writing ---

// logWrite appends one command record to the WAL; args includes the
// command name as its first element. Callers hold e.mu, which also
// serializes appends.
func (e *Engine) logWrite(args []string) error {
	if e.wal == nil {
		return nil
	}
	rec := encodeRecord(args)
	if err := e.wal.append(rec); err != nil {
		return fmt.Errorf("minikv: WAL append: %w", err)
	}
	return nil
}

func (w *walFile) append(payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.f.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.f.Write(payload); err != nil {
		return err
	}
	w.pending++
	if w.syncEvery > 0 && w.pending >= w.syncEvery {
		w.pending = 0
		return w.f.Sync()
	}
	return nil
}

func (w *walFile) syncAndClose() error {
	err := w.f.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// truncateTo drops everything after offset (a torn final record) and
// rewinds the write cursor so the next append lands exactly at the new
// end of file. Without this, post-recovery writes would land after the
// torn bytes and the next reopen would fail with a corruption error.
func (w *walFile) truncateTo(offset int64) error {
	if err := w.f.Truncate(offset); err != nil {
		return err
	}
	if _, err := w.f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	return w.f.Sync()
}

// --- WAL replay ---

// replay applies every complete record in order. An incomplete final
// record is dropped with a warning (torn tail) AND truncated away, so
// the engine can keep appending cleanly afterwards; corruption inside
// a complete record is a hard error because it means silent data
// damage, not just an interrupted write.
func (e *Engine) replay() error {
	r := bufio.NewReader(e.wal.f)
	var lastGood int64 // end offset of the last complete record
	for {
		payload, torn, err := nextRecord(r)
		if err != nil {
			return fmt.Errorf("minikv: %s: corrupt WAL: %w", e.wal.path, err)
		}
		if torn {
			fmt.Fprintf(os.Stderr, "minikv: %s: dropping torn final record\n", e.wal.path)
			if terr := e.wal.truncateTo(lastGood); terr != nil {
				return fmt.Errorf("minikv: %s: truncating torn tail: %w", e.wal.path, terr)
			}
			return nil
		}
		if payload == nil { // clean EOF
			return nil
		}
		args, err := decodeRecord(payload)
		if err != nil {
			return fmt.Errorf("minikv: %s: corrupt WAL record: %w", e.wal.path, err)
		}
		if _, err := e.exec(strings.ToUpper(args[0]), args[1:], false); err != nil {
			return fmt.Errorf("minikv: %s: corrupt WAL record: %w", e.wal.path, err)
		}
		lastGood += 4 + int64(len(payload))
	}
}

// nextRecord reads one length-prefixed record. Results: (nil,false,nil)
// at clean EOF, (nil,true,nil) on a torn tail, else the payload.
func nextRecord(r *bufio.Reader) (payload []byte, torn bool, err error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		switch err {
		case io.EOF:
			return nil, false, nil
		case io.ErrUnexpectedEOF:
			return nil, true, nil // length prefix itself cut in half
		default:
			return nil, false, err
		}
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxRecordLen {
		return nil, false, errors.New("record length exceeds maximum")
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		// ErrUnexpectedEOF = payload cut mid-write. Bare io.EOF here
		// means zero payload bytes arrived: the file was cut exactly
		// between the length header and the first payload byte. Both
		// are incomplete final records — a torn tail, not corruption.
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, true, nil
		}
		return nil, false, err
	}
	return payload, false, nil
}

// encodeRecord frames one command: u32 argc, then per-argument
// u32 length + raw bytes. Values may contain anything, including CRLF.
func encodeRecord(args []string) []byte {
	total := 4
	for _, a := range args {
		total += 4 + len(a)
	}
	buf := make([]byte, 4, total)
	binary.BigEndian.PutUint32(buf, uint32(len(args)))
	for _, a := range args {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(a)))
		buf = append(buf, l[:]...)
		buf = append(buf, a...)
	}
	return buf
}

func decodeRecord(p []byte) ([]string, error) {
	if len(p) < 4 {
		return nil, errors.New("short record")
	}
	n := binary.BigEndian.Uint32(p[:4])
	if n == 0 || n > 1024 {
		return nil, errors.New("bad argument count")
	}
	args := make([]string, 0, n)
	pos := 4
	for i := uint32(0); i < n; i++ {
		if pos+4 > len(p) {
			return nil, errors.New("truncated argument header")
		}
		l := binary.BigEndian.Uint32(p[pos : pos+4])
		pos += 4
		if l > maxRecordLen || int(l) > len(p)-pos {
			return nil, errors.New("truncated argument")
		}
		args = append(args, string(p[pos:pos+int(l)]))
		pos += int(l)
	}
	if pos != len(p) {
		return nil, errors.New("trailing bytes in record")
	}
	return args, nil
}

// --- lifecycle & background sweep ---

// Close stops the sweeper, syncs and closes the WAL. Idempotent.
// The sweeper is joined before taking e.mu (sweepOnce needs e.mu, so
// holding it across the join would deadlock); the WAL close itself
// happens under e.mu so no in-flight Apply can touch the file.
func (e *Engine) Close() error {
	var err error
	e.closeOnce.Do(func() {
		close(e.stop)
		<-e.sweepDone
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.wal != nil {
			err = e.wal.syncAndClose()
		}
	})
	return err
}

// kill simulates a process crash: the WAL file handle is abandoned
// without fsync or cleanup, under e.mu like Close. Test-only hook for
// crash-consistency tests.
func (e *Engine) kill() {
	e.closeOnce.Do(func() {
		close(e.stop)
		<-e.sweepDone
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.wal != nil {
			e.wal.f.Close()
		}
	})
}

// sweepLoop ticks once per second and deletes expired entries with
// bounded work per tick.
func (e *Engine) sweepLoop() {
	defer close(e.sweepDone)
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-t.C:
			e.sweepOnce()
		}
	}
}

func (e *Engine) sweepOnce() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	examined := 0
	for k, ent := range e.data {
		if examined >= maxSweepScan {
			break
		}
		examined++
		if !ent.expiresAt.IsZero() && !now.Before(ent.expiresAt) {
			e.deleteLocked(k)
		}
	}
}

// --- TCP server (RESP subset) ---

// Server serves the RESP-subset protocol over TCP using an Engine.
type Server struct {
	cfg    Config
	eng    *Engine
	ln     net.Listener
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// NewServer creates the backing engine and binds cfg.Addr immediately,
// so dialing is race-free right after NewServer returns.
func NewServer(cfg Config) (*Server, error) {
	eng, err := NewEngine(Config{
		WALPath:        cfg.WALPath,
		SyncEveryN:     cfg.SyncEveryN,
		MaxMemory:      cfg.MaxMemory,
		EvictionPolicy: cfg.EvictionPolicy,
	})
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		eng.Close()
		return nil, fmt.Errorf("minikv: listen %s: %w", cfg.Addr, err)
	}
	return &Server{cfg: cfg, eng: eng, ln: ln, conns: make(map[net.Conn]struct{})}, nil
}

// ListenAndServe accepts connections until Shutdown or Close; it
// returns nil after a clean stop.
func (s *Server) ListenAndServe() error {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if s.isClosed() {
				return nil
			}
			return fmt.Errorf("minikv: accept: %w", err)
		}
		// Register the conn and bump the WaitGroup under the same lock
		// so Shutdown can never observe the conn as tracked while its
		// serveConn goroutine is not yet counted (drain race).
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			continue
		}
		s.conns[c] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serveConn(c)
	}
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// serveConn handles one connection: parse RESP arrays, execute, reply.
// It exits on client hangup, protocol error, or server shutdown.
func (s *Server) serveConn(c net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.Close()
	}()

	r := bufio.NewReader(c)
	for {
		args, err := readCommand(r)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.Write([]byte("-ERR protocol error\r\n"))
			}
			return
		}
		reply := s.handle(args)
		if _, err := c.Write(reply); err != nil {
			return
		}
		if s.isClosed() { // finish the current command, then stop reading
			return
		}
	}
}

// handle executes one parsed command and returns exact wire bytes.
func (s *Server) handle(args []string) []byte {
	if len(args) == 0 {
		return []byte("-ERR empty command\r\n")
	}
	cmd := strings.ToUpper(args[0])

	// GET bypasses Apply so an empty stored value stays a real bulk
	// reply instead of colliding with the nil-bulk sentinel.
	if cmd == "GET" {
		if len(args) != 2 {
			return []byte("-" + errArity("GET").Error() + "\r\n")
		}
		v, ok := s.eng.Get(args[1])
		if !ok {
			return []byte("$-1\r\n")
		}
		return bulkReply(v)
	}

	out, err := s.eng.Apply(cmd, args[1:]...)
	if err != nil {
		return errReply(err)
	}
	switch cmd {
	case "PING":
		return []byte("+PONG\r\n")
	case "SET":
		return []byte("+OK\r\n")
	case "KEYS":
		return bulkReply(out) // newline-joined sorted keys, possibly empty
	default: // DEL, TTL: integer replies
		return []byte(":" + out + "\r\n")
	}
}

func bulkReply(v string) []byte {
	return []byte(fmt.Sprintf("$%d\r\n%s\r\n", len(v), v))
}

func errReply(err error) []byte {
	msg := strings.NewReplacer("\r", " ", "\n", " ").Replace(err.Error())
	return []byte("-" + msg + "\r\n")
}

// readCommand parses `*<n>\r\n` followed by n `$<len>\r\n<data>\r\n`
// bulk strings.
func readCommand(r *bufio.Reader) ([]string, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("expected array header, got %q", line)
	}
	n, err := strconv.Atoi(string(line[1:]))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("bad array length %q", line[1:])
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}
		if len(line) == 0 || line[0] != '$' {
			return nil, fmt.Errorf("expected bulk header, got %q", line)
		}
		l, err := strconv.Atoi(string(line[1:]))
		if err != nil || l < 0 || l > maxRecordLen {
			// Cap the allocation up front: a hostile or buggy client
			// must not be able to make us allocate from its say-so.
			return nil, fmt.Errorf("bad bulk length %q", line[1:])
		}
		buf := make([]byte, l+2) // payload + CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		if buf[l] != '\r' || buf[l+1] != '\n' {
			return nil, errors.New("bulk data not CRLF-terminated")
		}
		args = append(args, string(buf[:l]))
	}
	return args, nil
}

// readLine returns one line without its \r\n terminator.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	line = line[:len(line)-1]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, nil
}

// --- server lifecycle ---

// Shutdown stops accepting, drains in-flight connection work bounded by
// ctx, then closes the engine (and its WAL).
func (s *Server) Shutdown(ctx context.Context) error {
	s.beginClose()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.closeConns() // drain deadline exceeded: force readers awake
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	return s.eng.Close()
}

// Close is the fast shutdown path: it force-closes connections without
// draining.
func (s *Server) Close() error {
	s.beginClose()
	s.closeConns()
	s.wg.Wait()
	return s.eng.Close()
}

func (s *Server) beginClose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.ln.Close()
}

func (s *Server) closeConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.Close()
	}
}
