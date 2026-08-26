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
	switch s {
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Follower"
	}
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

type realClock struct{}

func (realClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.t.C }

func (t realTimer) Reset(d time.Duration) { t.t.Reset(d) }

func (t realTimer) Stop() { t.t.Stop() }

type config struct {
	heartbeat   time.Duration
	electionMin time.Duration
	electionMax time.Duration
	rpcTimeout  time.Duration
	clock       Clock
	rand        *rand.Rand
}

// Option configures a Node at construction time.
type Option func(*config)

// WithClock overrides the timer source (for deterministic tests).
func WithClock(c Clock) Option { return func(cg *config) { cg.clock = c } }

// WithRand injects the randomness source used to jitter election
// timeouts. Supplying a seeded source makes cluster schedules
// reproducible across runs.
func WithRand(r *rand.Rand) Option { return func(cg *config) { cg.rand = r } }

// WithTiming tunes heartbeat, election timeout bounds, and the client
// RPC timeout. Zero values keep the defaults.
func WithTiming(heartbeat, electionMin, electionMax, rpcTimeout time.Duration) Option {
	return func(cg *config) {
		if heartbeat > 0 {
			cg.heartbeat = heartbeat
		}
		if electionMin > 0 {
			cg.electionMin = electionMin
		}
		if electionMax > electionMin {
			cg.electionMax = electionMax
		}
		if rpcTimeout > 0 {
			cg.rpcTimeout = rpcTimeout
		}
	}
}

func newConfig(opts []Option) config {
	c := config{
		heartbeat:   defaultHeartbeat,
		electionMin: defaultElectionMin,
		electionMax: defaultElectionMax,
		rpcTimeout:  defaultRPCTimeout,
		clock:       realClock{},
	}
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

func (c *config) electionTimeout() time.Duration {
	span := c.electionMax - c.electionMin
	if c.rand != nil {
		return c.electionMin + time.Duration(c.rand.Int63n(int64(span)))
	}
	return c.electionMin + time.Duration(rand.Int63n(int64(span)))
}

// Status is an externally observable snapshot of node state, used by
// tests and clients to locate the leader.
type Status struct {
	ID       string
	Term     uint64
	State    State
	LeaderID string // "" while unknown (e.g. during an election)
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

type pendingReq struct {
	idx  uint64
	kind reqKind
	key  string
	ch   chan reqResult
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

// New returns a node with the given ID among ids. The transport must
// route messages between all nodes in the cluster. Start the node
// with Start before expecting any behavior.
func New(id string, ids []string, tr Transport, opts ...Option) *Node {
	peers := make([]string, 0, len(ids))
	for _, other := range ids {
		if other != id {
			peers = append(peers, other)
		}
	}
	return &Node{
		id:      id,
		peerIDs: peers,
		tr:      tr,
		cfg:     newConfig(opts),
		reqCh:   make(chan *request, 32),
	}
}

// ID returns the node's identifier.
func (n *Node) ID() string { return n.id }

// Start launches the node's event loop; it returns immediately. The
// loop exits when ctx is cancelled or the inbound transport closes.
func (n *Node) Start(ctx context.Context) {
	go n.run(ctx)
}

func (n *Node) publish(s State, term uint64, leaderID string) {
	n.mu.Lock()
	n.status = Status{ID: n.id, Term: term, State: s, LeaderID: leaderID}
	n.mu.Unlock()
}

func (n *Node) publishKV(kv map[string]string) {
	cp := make(map[string]string, len(kv))
	for k, v := range kv {
		cp[k] = v
	}
	n.mu.Lock()
	n.kvSnap = cp
	n.mu.Unlock()
}

func (n *Node) markStopped() {
	n.mu.Lock()
	n.stopped = true
	n.mu.Unlock()
}

// Status reports the node's latest observable role/term/leader.
func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status
}

// Snapshot returns a copy of the node's applied key/value state. It is
// an observability aid for tests, not part of the consensus contract.
func (n *Node) Snapshot() map[string]string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.kvSnap == nil {
		return map[string]string{}
	}
	cp := make(map[string]string, len(n.kvSnap))
	for k, v := range n.kvSnap {
		cp[k] = v
	}
	return cp
}

// Set proposes a key/value write. It blocks until the entry is
// committed by a quorum, then returns nil. If this node is not the
// leader it fails fast with ErrNotLeader; if quorum is unreachable it
// fails with ErrNoQuorum after the RPC timeout.
func (n *Node) Set(key, val string) error {
	_, _, err := n.call(request{kind: reqSet, key: key, val: val})
	return err
}

// Get reads through the leader using a committed read barrier, so the
// result reflects all previously acknowledged writes (linearizable
// reads at teaching scale). Non-leaders return ErrNotLeader.
func (n *Node) Get(key string) (string, bool, error) {
	return n.call(request{kind: reqGet, key: key})
}

func (n *Node) call(req request) (string, bool, error) {
	n.mu.Lock()
	stopped := n.stopped
	n.mu.Unlock()
	if stopped {
		return "", false, ErrStopped
	}
	req.resp = make(chan reqResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), n.cfg.rpcTimeout)
	defer cancel()
	select {
	case n.reqCh <- &req:
	case <-ctx.Done():
		return "", false, ErrNoQuorum
	}
	select {
	case res := <-req.resp:
		return res.val, res.ok, res.err
	case <-ctx.Done():
		return "", false, ErrNoQuorum
	}
}
