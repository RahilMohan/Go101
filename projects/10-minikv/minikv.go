// Package minikv is a small Redis-like key/value store: a RESP-subset
// binary protocol served over TCP, a write-ahead log with configurable
// fsync batching, lazy + active TTL expiry, and LRU eviction under a
// memory cap. It teaches binary wire protocols, length-prefixed
// append-only logs, crash-consistency testing against torn tail
// writes, fsync durability tradeoffs, TTL sweep loops with bounded
// work, and approximate LRU eviction.
package minikv

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// Config configures an Engine or Server.
type Config struct {
	Addr           string // TCP listen address (Server only)
	WALPath        string // empty = in-memory only (no persistence)
	SyncEveryN     int    // fsync every N WAL writes; 1=durability per write, 0=batched by the OS
	MaxMemory      int64  // bytes; 0 = unlimited; when exceeded, evict until under
	EvictionPolicy string // "none" (default) | "lru"
}

// entryOverhead approximates the per-key memory cost beyond key+value
// bytes (map slot, value header, expiry/access timestamps).
const entryOverhead int64 = 48

// Engine is the testable core: a concurrent map of entries with TTL
// metadata, a WAL appender, and a bounded active-expiry sweeper.
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

type entry struct {
	value      string
	expiresAt  time.Time // zero = no expiry
	lastAccess time.Time
}

// walFile appends length-prefixed records to an on-disk log and owns
// the fsync batching counter.
type walFile struct {
	f         *os.File
	path      string
	syncEvery int
	pending   int
}

// ceilSeconds rounds a duration up to whole seconds.
func ceilSeconds(d time.Duration) int64 {
	// TODO: Implement
	return 0
}

// NewEngine creates an engine, opening (and replaying) the WAL when
// cfg.WALPath is set, and starts the active TTL sweeper.
func NewEngine(cfg Config) (*Engine, error) {
	// TODO: Implement
	return nil, errors.New("minikv: not implemented")
}

// Apply executes one command (PING, SET, GET, DEL, KEYS, TTL) and
// appends it to the WAL when the engine is persistent. Replies are the
// plain-text payload of the RESP reply: "PONG"/"OK" for PING/SET, the
// value for GET ("" = nil bulk), "0"/"1" for DEL, newline-joined keys
// for KEYS, and "-2"/"-1"/seconds for TTL.
func (e *Engine) Apply(cmd string, args ...string) (string, error) {
	// TODO: Implement
	return "", nil
}

// Get returns the live value for key, applying lazy expiry.
func (e *Engine) Get(key string) (string, bool) {
	// TODO: Implement
	return "", false
}

// Keys returns the sorted list of live keys.
func (e *Engine) Keys() []string {
	// TODO: Implement
	return nil
}

// TTL returns the time remaining before key expires; false if the key
// is missing or has no expiry.
func (e *Engine) TTL(key string) (time.Duration, bool) {
	// TODO: Implement
	return 0, false
}

// Close stops the sweeper, syncs and closes the WAL. Idempotent.
func (e *Engine) Close() error {
	// TODO: Implement
	return nil
}

// kill simulates a process crash: the WAL file handle is abandoned
// without fsync or cleanup. Test-only hook for crash-consistency tests.
func (e *Engine) kill() {
	// TODO: Implement
}

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
	// TODO: Implement
	return nil, errors.New("minikv: not implemented")
}

// ListenAndServe accepts connections until Shutdown or Close; it
// returns nil after a clean stop.
func (s *Server) ListenAndServe() error {
	// TODO: Implement
	return nil
}

// Shutdown stops accepting, drains in-flight connection work bounded by
// ctx, then closes the engine (and its WAL).
func (s *Server) Shutdown(ctx context.Context) error {
	// TODO: Implement
	return nil
}

// Close is the fast shutdown path: it force-closes connections without
// draining.
func (s *Server) Close() error {
	// TODO: Implement
	return nil
}
