// Package pubsubx provides a generic, thread-safe in-memory
// publish/subscribe broker. It teaches Go type parameters (generics),
// buffered-channel semantics, backpressure strategies (drop-new,
// drop-old, block), and subscriber isolation: a slow or stalled
// consumer must never prevent other subscribers from receiving
// messages, nor block the publisher.
package pubsubx

import "context"

// Policy selects what happens when a subscriber's buffer is full.
type Policy int

const (
	// DropNew drops the incoming message when the buffer is full.
	DropNew Policy = iota
	// DropOld evicts the oldest queued message to admit the new one.
	DropOld
	// Block makes the caller wait until the message is admitted or the
	// context is done. Only honored by PublishBlocking.
	Block
)

// Broker distributes messages of type T to subscribers.
type Broker[T any] struct {
	_ struct{}
}

// New returns an empty broker whose default backpressure policy is
// DropNew.
func New[T any]() *Broker[T] {
	// TODO: Implement
	return &Broker[T]{}
}

// Subscribe registers a subscriber with per-subscriber capacity cap
// (clamped to a minimum of 1). Returns an unsubscribe func that is
// idempotent and safe for concurrent use; after it returns, no further
// sends to this subscriber's channel will occur and the channel is
// closed.
func (b *Broker[T]) Subscribe(capacity int) (<-chan T, func()) {
	// TODO: Implement
	return nil, nil
}

// Publish delivers msg to every subscriber WITHOUT blocking on slow
// ones: each subscriber's buffer is filled if room, otherwise the
// message is dropped for that subscriber only. Returns the number of
// subscribers the message was delivered to. Publish after Close is a
// no-op returning 0.
func (b *Broker[T]) Publish(msg T) int {
	// TODO: Implement
	return 0
}

// SetDefaultPolicy changes the backpressure policy applied to
// subsequently created subscribers.
func (b *Broker[T]) SetDefaultPolicy(p Policy) {
	// TODO: Implement
}

// PublishBlocking delivers msg like Publish, except that subscribers
// created with the Block policy are waited on until they admit the
// message or ctx is done. If any blocked subscriber could not be
// admitted before ctx was done, returns ctx.Err(); otherwise nil.
func (b *Broker[T]) PublishBlocking(ctx context.Context, msg T) error {
	// TODO: Implement
	return nil
}

// Close closes all subscriber channels and makes all subsequent
// Publish calls no-ops returning 0. Close is idempotent, and
// unsubscribing after Close is safe.
func (b *Broker[T]) Close() {
	// TODO: Implement
}
