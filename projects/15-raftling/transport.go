package raft

import (
	"math/rand"
	"sync"
	"time"
)

// Transport carries Raft messages between nodes. Send must be safe for
// concurrent use; delivery is asynchronous. A transport may drop,
// delay, or reorder messages — Raft correctness must not depend on
// reliable ordered delivery.
type Transport interface {
	Send(m Message)
	Recv() <-chan Message
}

// Hub is an in-memory message bus connecting a fixed set of node IDs.
// Each node gets a private, buffered inbox; Send routes by Message.To.
// Close closes every inbox, which unblocks node event loops.
type Hub struct {
	mu    sync.Mutex
	boxes map[string]chan Message
}

// NewHub returns a hub with one inbox per provided ID.
func NewHub(ids ...string) *Hub {
	// TODO: Implement
	return &Hub{}
}

// Transport returns the transport endpoint for the given node ID.
func (h *Hub) Transport(id string) Transport {
	// TODO: Implement
	return nil
}

// Close closes all inboxes. Receivers observe a closed Recv channel.
func (h *Hub) Close() {
	// TODO: Implement
}

// Policy configures deterministic fault injection for WrapTransport.
// All fields are optional; the zero value passes every message through
// unchanged. Change faults while a cluster is running via
// FaultTransport.Mutate — direct writes would be racy.
type Policy struct {
	// DropAll drops every message sent through the wrapper.
	DropAll bool
	// DropProb in [0,1) gives the per-message drop probability.
	DropProb float64
	// Delay adds artificial latency to each delivered message.
	Delay time.Duration
	// Reorder swaps each adjacent pair of messages before delivery.
	Reorder bool
	// Rand is the source used for DropProb decisions. It is required
	// when DropProb > 0; supply a seeded rand.Rand for determinism.
	Rand *rand.Rand
}

// FaultTransport wraps another Transport with the fault rules of its
// policy, in both directions: outbound Send and inbound Recv are both
// subject to drop/delay/reorder. Use Flush to force-deliver a message
// held by Reorder pairing, and Mutate to change faults at runtime.
type FaultTransport struct {
	inner Transport
	pol   *Policy
}

// WrapTransport returns inner guarded by p's fault-injection rules.
// A nil policy returns inner unchanged.
func WrapTransport(inner Transport, p *Policy) *FaultTransport {
	// TODO: Implement
	return &FaultTransport{inner: inner, pol: p}
}

// Mutate applies fn to the live policy under the wrapper's lock. This
// is the race-free way to toggle faults (e.g. partition a link) while
// the cluster is running.
func (f *FaultTransport) Mutate(fn func(*Policy)) {
	// TODO: Implement
}

// Flush delivers any message currently held back by Reorder pairing.
func (f *FaultTransport) Flush() {
	// TODO: Implement
}

func (f *FaultTransport) Send(m Message) {
	// TODO: Implement
}

func (f *FaultTransport) Recv() <-chan Message {
	// TODO: Implement
	return nil
}
