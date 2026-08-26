// Package raft implements teaching-scale Raft consensus: leader
// election, log replication, and commit advancement over pluggable
// in-memory transports. It is deliberately minimal and is NOT
// production-grade.
//
// Out of scope (documented limitations):
//
//   - No persistence or snapshotting: a restarted node loses all state.
//   - No cluster membership changes: clusters are static.
//   - No log compaction: logs grow unbounded.
package raft

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// Errors returned by client operations.
var (
	// ErrNotLeader means this node cannot serve the request; retry
	// against the current leader (see Status).
	ErrNotLeader = errors.New("raft: not the leader")
	// ErrNoQuorum means the request could not be committed within the
	// RPC timeout — typically because the node is partitioned away
	// from the majority.
	ErrNoQuorum = errors.New("raft: no quorum reached")
	// ErrStopped means the node's event loop has shut down.
	ErrStopped = errors.New("raft: node stopped")
	// ErrBusy means the node's local request queue was full — client
	// backpressure on this node, not a quorum problem. Retry shortly
	// or route to another node.
	ErrBusy = errors.New("raft: request queue full")
)

const (
	defaultHeartbeat   = 30 * time.Millisecond
	defaultElectionMin = 150 * time.Millisecond
	defaultElectionMax = 300 * time.Millisecond
	defaultRPCTimeout  = 2 * time.Second
)

// State is a node's Raft role.
type State int

// Node roles.
const (
	Follower State = iota
	Candidate
	Leader
)

// String implements fmt.Stringer.
func (s State) String() string {
	// TODO: Implement
	return ""
}

// Op discriminates log entry payloads.
type Op int

// Entry operations. OpRead is a linearizability barrier: it carries a
// key but writes nothing, and its reply reports the applied value.
const (
	OpSet Op = iota
	OpRead
)

// Entry is one replicated key/value command.
type Entry struct {
	Index uint64
	Term  uint64
	Op    Op
	Key   string
	Value string
}

// MsgType enumerates Raft RPC kinds.
type MsgType int

// RPC kinds.
const (
	MsgRequestVote MsgType = iota
	MsgRequestVoteResp
	MsgAppendEntries
	MsgAppendEntriesResp
)

// Message is the single wire format for all Raft RPCs. Fields are
// interpreted per Type; unused fields stay zero.
type Message struct {
	Type MsgType
	From string
	To   string
	Term uint64

	// MsgRequestVote
	CandidateID  string
	LastLogIndex uint64
	LastLogTerm  uint64

	// MsgRequestVoteResp
	VoteGranted bool

	// MsgAppendEntries
	PrevLogIndex uint64
	PrevLogTerm  uint64
	LeaderCommit uint64
	Entries      []Entry

	// MsgAppendEntriesResp: on success, MatchIndex is the highest log
	// index known replicated on the responder; on failure it is a
	// conflict hint the leader can jump nextIndex to.
	MatchIndex uint64
	Success    bool
}

// Clock creates timers for elections and heartbeats. Injectable so
// tests can drive time deterministically.
type Clock interface {
	NewTimer(d time.Duration) Timer
}

// Timer is the subset of time.Timer the event loop needs.
type Timer interface {
	C() <-chan time.Time
	Reset(d time.Duration)
	Stop()
}

// Option configures a Node at construction time.
type Option func(*config)

type config struct {
	heartbeat   time.Duration
	electionMin time.Duration
	electionMax time.Duration
	rpcTimeout  time.Duration
	clock       Clock
	rand        *rand.Rand
}

// WithClock overrides the timer source (for deterministic tests).
func WithClock(c Clock) Option { return func(cg *config) { cg.clock = c } }

// WithRand injects the randomness source used to jitter election
// timeouts. Supplying a seeded source makes cluster schedules
// reproducible across runs.
func WithRand(r *rand.Rand) Option { return func(cg *config) { cg.rand = r } }

// WithTiming tunes heartbeat, election timeout bounds, and the client
// RPC timeout. Zero values keep the defaults.
func WithTiming(heartbeat, electionMin, electionMax, rpcTimeout time.Duration) Option {
	return func(cg *config) { // TODO: honor overrides
		_ = heartbeat
		_ = electionMin
		_ = electionMax
		_ = rpcTimeout
	}
}

// Status is an externally observable snapshot of node state, used by
// tests and clients to locate the leader.
type Status struct {
	ID       string
	Term     uint64
	State    State
	LeaderID string // "" while unknown (e.g. during an election)
}

// Node is a single Raft replica. All Raft state is owned by the
// goroutine started by Start; client methods communicate with it via
// an internal request channel, and observability fields are published
// under a small lock.
type Node struct {
	id      string
	peerIDs []string
	tr      Transport
	cfg     config
	reqCh   chan *request

	mu      sync.Mutex
	status  Status
	kvSnap  map[string]string
	stopped bool
}

type reqKind int

const (
	reqSet reqKind = iota
	reqGet
)

type request struct {
	kind reqKind
	key  string
	val  string
	resp chan reqResult
}

type reqResult struct {
	val string
	ok  bool
	err error
}

// New returns a node with the given ID among ids. The transport must
// route messages between all nodes in the cluster. Start the node
// with Start before expecting any behavior.
func New(id string, ids []string, tr Transport, opts ...Option) *Node {
	// TODO: Implement
	return &Node{}
}

// ID returns the node's identifier.
func (n *Node) ID() string {
	// TODO: Implement
	return ""
}

// Start launches the node's event loop; it returns immediately. The
// loop exits when ctx is cancelled or the inbound transport closes.
func (n *Node) Start(ctx context.Context) {
	// TODO: Implement
}

// Status reports the node's latest observable role/term/leader.
func (n *Node) Status() Status {
	// TODO: Implement
	return Status{}
}

// Snapshot returns a copy of the node's applied key/value state. It is
// an observability aid for tests, not part of the consensus contract.
func (n *Node) Snapshot() map[string]string {
	// TODO: Implement
	return nil
}

// Set proposes a key/value write. It blocks until the entry is
// committed by a quorum, then returns nil. If this node is not the
// leader it fails fast with ErrNotLeader; if quorum is unreachable it
// fails with ErrNoQuorum after the RPC timeout.
func (n *Node) Set(key, val string) error {
	// TODO: Implement
	return nil
}

// Get reads through the leader using a committed read barrier, so the
// result reflects all previously acknowledged writes (linearizable
// reads at teaching scale). Non-leaders return ErrNotLeader.
func (n *Node) Get(key string) (string, bool, error) {
	// TODO: Implement
	return "", false, nil
}
