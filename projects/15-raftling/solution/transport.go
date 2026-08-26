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
	mu     sync.Mutex
	boxes  map[string]chan Message
	closed bool
}

// NewHub returns a hub with one inbox per provided ID.
func NewHub(ids ...string) *Hub {
	h := &Hub{boxes: make(map[string]chan Message, len(ids))}
	for _, id := range ids {
		h.boxes[id] = make(chan Message, 512)
	}
	return h
}

// Transport returns the transport endpoint for the given node ID.
func (h *Hub) Transport(id string) Transport { return &hubTransport{hub: h, id: id} }

// Close closes all inboxes. Receivers observe a closed Recv channel.
// Sends after Close are silently dropped.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, ch := range h.boxes {
		close(ch)
	}
}

func (h *Hub) box(to string) chan Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.boxes[to]
}

type hubTransport struct {
	hub *Hub
	id  string
}

func (t *hubTransport) Send(m Message) {
	// Send under the hub lock so Close can never close an inbox that
	// a sender is concurrently writing to.
	h := t.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	box, ok := h.boxes[m.To]
	if !ok || box == nil || h.closed {
		return
	}
	select {
	case box <- m:
	default:
		// Inbox full: drop rather than deadlock the sender.
	}
}

func (t *hubTransport) Recv() <-chan Message {
	return t.hub.box(t.id)
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

	once    sync.Once
	out     chan Message
	mu      sync.Mutex
	n       uint64
	pending *Message
}

// WrapTransport returns inner guarded by p's fault-injection rules.
// A nil policy returns inner unchanged.
func WrapTransport(inner Transport, p *Policy) *FaultTransport {
	if p == nil {
		p = &Policy{}
	}
	return &FaultTransport{inner: inner, pol: p}
}

// Mutate applies fn to the live policy under the wrapper's lock. This
// is the race-free way to toggle faults (e.g. partition a link) while
// the cluster is running.
func (f *FaultTransport) Mutate(fn func(*Policy)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.pol)
}

// observe snapshots the drop decision and delay for one message.
func (f *FaultTransport) observe() (drop bool, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	drop = f.pol.DropAll
	if !drop && f.pol.DropProb > 0 {
		if f.pol.Rand == nil {
			panic("raft: Policy.DropProb requires Policy.Rand")
		}
		drop = f.pol.Rand.Float64() < f.pol.DropProb
	}
	return drop, f.pol.Delay
}

func (f *FaultTransport) pump() {
	for m := range f.inner.Recv() {
		drop, delay := f.observe()
		if drop {
			continue
		}
		if delay <= 0 {
			f.forward(m)
			continue
		}
		go func(m Message) {
			time.Sleep(delay)
			f.forward(m)
		}(m)
	}
	close(f.out)
}

func (f *FaultTransport) forward(m Message) {
	select {
	case f.out <- m:
	default:
	}
}

func (f *FaultTransport) Recv() <-chan Message {
	f.once.Do(func() {
		f.out = make(chan Message, 512)
		go f.pump()
	})
	return f.out
}

// Flush delivers any message currently held back by Reorder pairing.
func (f *FaultTransport) Flush() {
	f.mu.Lock()
	held := f.pending
	f.pending = nil
	delay := f.pol.Delay
	f.mu.Unlock()
	if held != nil {
		f.deliver(*held, delay)
	}
}

func (f *FaultTransport) Send(m Message) {
	drop, delay := f.observe()
	if drop {
		return
	}
	f.mu.Lock()
	if f.pol.Reorder {
		if f.pending != nil {
			first := *f.pending
			f.pending = nil
			f.mu.Unlock()
			f.deliver(m, delay)     // newer message first ...
			f.deliver(first, delay) // ... then the older one: pair swapped.
			return
		}
		cp := m
		f.pending = &cp // held until its pair arrives (or Flush).
		f.mu.Unlock()
		return
	}
	f.mu.Unlock()
	f.deliver(m, delay)
}

func (f *FaultTransport) deliver(m Message, delay time.Duration) {
	if delay <= 0 {
		f.inner.Send(m)
		return
	}
	go func() {
		time.Sleep(delay)
		f.inner.Send(m)
	}()
}
