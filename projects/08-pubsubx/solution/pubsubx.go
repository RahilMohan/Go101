// Package pubsubx provides a generic, thread-safe in-memory
// publish/subscribe broker. It teaches Go type parameters (generics),
// buffered-channel semantics, backpressure strategies (drop-new,
// drop-old, block), and subscriber isolation: a slow or stalled
// consumer must never prevent other subscribers from receiving
// messages, nor block the publisher.
//
// # Concurrency design: fine-grained mutexes (the justification)
//
// This implementation uses a two-level locking scheme instead of a
// single owner goroutine:
//
//   - Broker.mu serializes broker-level state (the subscriber map,
//     the default policy, the closed flag). Because Publish holds it
//     for the whole fan-out, only one publisher mutates a channel
//     buffer at a time — which is exactly what makes DropOld's
//     "evict one, then send" sequence race-free.
//   - subscriber.mu guards each subscriber's closed flag. Every send
//     holds this lock in READ mode for the duration of the send,
//     including any blocking select. Closing a channel takes the same
//     lock in WRITE mode. Since a close can never run while a sender
//     is mid-send, send-on-closed-channel panics are impossible under
//     any interleaving of Subscribe/Publish/Unsubscribe/Close.
//
// A single owner goroutine was considered and rejected: Publish would
// need a reply channel per call to report the delivered count, and
// PublishBlocking would need careful multiplexing of many waiters on
// one goroutine — more moving parts than the mutex scheme, with no
// added safety. The cost paid here is that Close waits for in-flight
// blocking sends; those are already bounded by the caller's ctx.
package solution

import (
	"context"
	"sync"
	"time"
)

// Policy selects what happens when a subscriber's buffer is full.
type Policy int

const (
	// DropNew drops the incoming message when the buffer is full.
	// This is the default Publish behavior.
	DropNew Policy = iota
	// DropOld evicts the oldest queued message to admit the new one,
	// keeping the buffer at capacity with the freshest messages.
	DropOld
	// Block makes the caller wait until the message is admitted or
	// the context is done. Only honored by PublishBlocking; plain
	// Publish treats Block subscribers like DropNew so that it can
	// never block.
	Block
)

// pollInterval is how long PublishBlocking waits between attempts to
// squeeze a message into a full buffer. The broker cannot observe a
// consumer receiving from its channel (consumers read the returned
// channel directly), so admission polling is the simplest correct
// wakeup strategy; the interval is short enough to feel immediate.
const pollInterval = 250 * time.Microsecond

// subscriber is one registered consumer: a buffered channel plus the
// policy captured from the broker default at Subscribe time.
type subscriber[T any] struct {
	ch     chan T
	policy Policy

	// mu guards closed. Senders hold it in read mode across every
	// send attempt (including blocking waits); the closer holds it in
	// write mode around close(ch). See the package comment.
	mu     sync.RWMutex
	closed bool
}

// deliver attempts a non-blocking send under the drop policy.
// Called with Broker.mu held, so concurrent publishers cannot interleave
// with the evict-then-send sequence below.
func (s *subscriber[T]) deliver(msg T) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return false // lost the race with Unsubscribe/Close
	}
	select {
	case s.ch <- msg:
		return true
	default:
	}
	if s.policy != DropOld {
		return false // DropNew (and Block, under non-blocking Publish)
	}
	// Buffer full and the policy is DropOld: make room by evicting the
	// oldest element, then send. Consumers only ever remove elements,
	// and no other publisher can add them (Broker.mu is held), so after
	// a successful eviction there is guaranteed room for the new value.
	select {
	case <-s.ch:
	default:
	}
	select {
	case s.ch <- msg:
		return true
	default:
		return false // defensive: unreachable given the invariants above
	}
}

// blockingDeliver repeatedly attempts to send until admitted, removed,
// or ctx is done. It reports whether the message was admitted (a
// subscriber disappearing counts as admitted: there is nobody left to
// wait for). A nil ctx is treated as context.Background() (blocks until
// admitted or subscriber vanishes) and is documented on PublishBlocking.
func (s *subscriber[T]) blockingDeliver(ctx context.Context, msg T) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	t := time.NewTimer(pollInterval)
	defer t.Stop()
	for {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return true
		}
		select {
		case s.ch <- msg:
			s.mu.RUnlock()
			return true
		default:
			s.mu.RUnlock()
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			t.Reset(pollInterval)
		}
	}
}

// Broker distributes messages of type T to subscribers.
type Broker[T any] struct {
	mu       sync.Mutex
	subs     map[int64]*subscriber[T]
	nextID   int64
	policy   Policy
	isClosed bool
}

// New returns an empty broker whose default backpressure policy is
// DropNew.
func New[T any]() *Broker[T] {
	return &Broker[T]{subs: make(map[int64]*subscriber[T])}
}

// Subscribe registers a subscriber whose channel has the given buffer
// capacity (clamped to a minimum of 1). The subscriber captures the
// broker's current default policy. The returned unsubscribe func is
// idempotent and safe for concurrent use; after it returns, no further
// sends to this subscriber's channel will occur and the channel is
// closed. Subscribing to a closed broker yields an already-closed
// channel and a no-op func.
func (b *Broker[T]) Subscribe(capacity int) (<-chan T, func()) {
	if capacity < 1 {
		capacity = 1
	}
	b.mu.Lock()
	if b.isClosed {
		b.mu.Unlock()
		ch := make(chan T, 1)
		close(ch)
		var noop sync.Once
		return ch, func() { noop.Do(func() {}) }
	}
	b.nextID++
	id := b.nextID
	sub := &subscriber[T]{
		ch:     make(chan T, capacity),
		policy: b.policy,
	}
	b.subs[id] = sub
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			// Remove from the map first so no future Publish sees this
			// subscriber, then take the write lock to close the channel.
			// Any sender still inside deliver/blockingDeliver holds the
			// read lock, so the close below cannot overtake it.
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()

			sub.mu.Lock()
			if !sub.closed {
				sub.closed = true
				close(sub.ch)
			}
			sub.mu.Unlock()
		})
	}
	return sub.ch, unsubscribe
}

// Publish delivers msg to every subscriber without blocking on slow
// ones: each subscriber's buffer is filled if there is room, otherwise
// the message is dropped for that subscriber only (per its policy).
// It returns the number of subscribers the message was delivered to.
// Publishing to a closed broker is a no-op returning 0.
func (b *Broker[T]) Publish(msg T) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.isClosed {
		return 0
	}
	delivered := 0
	for _, sub := range b.subs {
		if sub.deliver(msg) {
			delivered++
		}
	}
	return delivered
}

// SetDefaultPolicy changes the backpressure policy applied to
// subsequently created subscribers; existing subscribers keep the
// policy they were created with. Unknown policies fall back to DropNew.
func (b *Broker[T]) SetDefaultPolicy(p Policy) {
	if p < DropNew || p > Block {
		p = DropNew
	}
	b.mu.Lock()
	b.policy = p
	b.mu.Unlock()
}

// PublishBlocking delivers msg like Publish, except that subscribers
// created with the Block policy are waited on until they admit the
// message or ctx is done. Non-blocking-policy subscribers get their
// ordinary drop semantics and do not gate completion. If any blocked
// subscriber could not be admitted before ctx was done, PublishBlocking
// returns ctx.Err(); otherwise it returns nil. A nil ctx is treated as
// context.Background() (i.e., block until admitted or subscriber
// vanishes); callers that need cancellation must pass a non-nil context.
func (b *Broker[T]) PublishBlocking(ctx context.Context, msg T) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	if b.isClosed {
		b.mu.Unlock()
		return nil
	}
	blocking := make([]*subscriber[T], 0, len(b.subs))
	for _, sub := range b.subs {
		sub.mu.RLock()
		isClosed := sub.closed
		isBlock := sub.policy == Block
		sub.mu.RUnlock()
		if isBlock && !isClosed {
			blocking = append(blocking, sub)
			continue
		}
		sub.deliver(msg)
	}
	b.mu.Unlock()

	var wg sync.WaitGroup
	var failed bool
	var mu sync.Mutex
	for _, sub := range blocking {
		wg.Add(1)
		go func(sub *subscriber[T]) {
			defer wg.Done()
			if !sub.blockingDeliver(ctx, msg) {
				mu.Lock()
				failed = true
				mu.Unlock()
			}
		}(sub)
	}
	wg.Wait()
	if failed {
		if err := ctx.Err(); err != nil {
			return err
		}
		return context.Canceled
	}
	return nil
}

// Close closes all subscriber channels and makes all subsequent
// Publish calls no-ops returning 0. Close is idempotent, and
// unsubscribing after Close is safe. Messages already buffered in a
// subscriber's channel remain readable after Close.
func (b *Broker[T]) Close() {
	b.mu.Lock()
	if b.isClosed {
		b.mu.Unlock()
		return
	}
	b.isClosed = true
	subs := make([]*subscriber[T], 0, len(b.subs))
	for _, sub := range b.subs {
		subs = append(subs, sub)
	}
	b.subs = make(map[int64]*subscriber[T])
	b.mu.Unlock()

	for _, sub := range subs {
		sub.mu.Lock()
		if !sub.closed {
			sub.closed = true
			close(sub.ch)
		}
		sub.mu.Unlock()
	}
}
