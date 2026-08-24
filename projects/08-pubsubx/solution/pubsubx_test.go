package solution

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// recvTimeout receives one value from ch or fails the test.
func recvTimeout[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v, ok := <-ch:
		if !ok {
			t.Fatalf("%s: channel unexpectedly closed", what)
		}
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: timed out waiting for value", what)
		var zero T
		return zero
	}
}

// assertEmpty fails if ch yields a value right now (a closed-and-empty
// channel counts as empty).
func assertEmpty[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v, ok := <-ch:
		if !ok {
			return // closed: no further values by definition
		}
		t.Fatalf("%s: unexpected extra value %v", what, v)
	default:
	}
}

type point struct{ X, Y int }

// ---------------------------------------------------------------------------
// 1. fan-out completeness and per-subscriber FIFO order
// ---------------------------------------------------------------------------

func TestFanOutCompletenessPreservesOrder(t *testing.T) {
	b := New[int]()
	defer b.Close()

	const nSubs, nMsgs = 3, 50
	chans := make([]<-chan int, nSubs)
	for i := range chans {
		ch, unsub := b.Subscribe(100)
		if ch == nil || unsub == nil {
			t.Fatal("Subscribe returned nil")
		}
		t.Cleanup(unsub)
		chans[i] = ch
	}

	for m := 0; m < nMsgs; m++ {
		if got := b.Publish(m); got != nSubs {
			t.Fatalf("Publish(%d) delivered to %d subscribers, want %d", m, got, nSubs)
		}
	}

	for i, ch := range chans {
		for m := 0; m < nMsgs; m++ {
			if got := recvTimeout(t, ch, "fan-out"); got != m {
				t.Fatalf("sub %d: got %d at position %d, want %d (FIFO broken)", i, got, m, m)
			}
		}
		assertEmpty(t, ch, "fan-out")
	}
}

// ---------------------------------------------------------------------------
// 2. subscriber isolation: a stalled slow sub cannot hurt others
// ---------------------------------------------------------------------------

func TestSlowSubscriberIsolation(t *testing.T) {
	b := New[int]()
	defer b.Close()

	fastCh, fastUnsub := b.Subscribe(300)
	defer fastUnsub()
	slowCh, slowUnsub := b.Subscribe(1) // never read: stalls immediately
	defer slowUnsub()

	const total = 200
	delivered := 0
	for m := 0; m < total; m++ {
		delivered += b.Publish(m)
	}

	for m := 0; m < total; m++ {
		if got := recvTimeout(t, fastCh, "fast sub"); got != m {
			t.Fatalf("fast sub: got %d at position %d, want %d", got, m, m)
		}
	}

	slowDelivered := delivered - total
	if slowDelivered < 0 || slowDelivered >= total {
		t.Fatalf("delivered=%d for %d publishes: slow sub received %d, want 0 < drops < %d",
			delivered, total, slowDelivered, total)
	}
	if slowDelivered != 1 { // cap-1 unread buffer admits exactly the first message
		t.Fatalf("slow sub admitted %d messages into cap-1 buffer, want 1", slowDelivered)
	}
	if v := recvTimeout(t, slowCh, "slow sub"); v != 0 {
		t.Fatalf("slow sub buffered value = %d, want 0 (the first published message)", v)
	}
}

// ---------------------------------------------------------------------------
// 3. DropOld evicts oldest, keeps newest, size stays at capacity
// ---------------------------------------------------------------------------

func TestDropOldEvictsOldestKeepsNewest(t *testing.T) {
	b := New[int]()
	defer b.Close()
	b.SetDefaultPolicy(DropOld)

	ch, unsub := b.Subscribe(3)
	defer unsub()

	for m := 1; m <= 5; m++ {
		if got := b.Publish(m); got != 1 {
			t.Fatalf("Publish(%d) delivered=%d, want 1", m, got)
		}
	}

	for _, want := range []int{3, 4, 5} {
		if got := recvTimeout(t, ch, "drop-old"); got != want {
			t.Fatalf("got %d, want %d (oldest should have been evicted)", got, want)
		}
	}
	assertEmpty(t, ch, "drop-old")
}

// ---------------------------------------------------------------------------
// 4. PublishBlocking: cancellation vs admission
// ---------------------------------------------------------------------------

func TestPublishBlockingReturnsOnCtxCancel(t *testing.T) {
	b := New[string]()
	defer b.Close()
	b.SetDefaultPolicy(Block)

	ch, unsub := b.Subscribe(1)
	defer unsub()
	b.Publish("fill") // buffer now full

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- b.PublishBlocking(ctx, "overflow") }()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("PublishBlocking err = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PublishBlocking did not return within bound after ctx cancellation (deadlock?)")
	}
	// The pre-filled message legitimately remains buffered; the blocked
	// one must not have been admitted.
	if v := recvTimeout(t, ch, "blocked publish"); v != "fill" {
		t.Fatalf("buffered value = %q, want %q (overflow must stay dropped)", v, "fill")
	}
	assertEmpty(t, ch, "blocked publish")
}

func TestPublishBlockingAdmitsWhenSpaceFrees(t *testing.T) {
	b := New[int]()
	defer b.Close()
	b.SetDefaultPolicy(Block)

	ch, unsub := b.Subscribe(1)
	defer unsub()
	b.Publish(1) // fill

	go func() {
		time.Sleep(50 * time.Millisecond)
		<-ch // free a slot
	}()

	start := time.Now()
	if err := b.PublishBlocking(context.Background(), 2); err != nil {
		t.Fatalf("PublishBlocking: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("PublishBlocking took %v to admit, suspiciously long", elapsed)
	}
	if got := recvTimeout(t, ch, "admission"); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}
}

func TestPublishBlockingDoesNotGateOnNonBlockSubs(t *testing.T) {
	b := New[int]()
	defer b.Close()

	dropCh, dropUnsub := b.Subscribe(1) // default policy: DropNew
	defer dropUnsub()
	b.SetDefaultPolicy(Block)
	blockCh, blockUnsub := b.Subscribe(4)
	defer blockUnsub()

	b.Publish(9) // fills both buffers

	// The drop-policy sub is full and stalled; PublishBlocking must still
	// return nil promptly because only Block subs gate completion.
	done := make(chan error, 1)
	go func() { done <- b.PublishBlocking(context.Background(), 7) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PublishBlocking err = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PublishBlocking gated on non-Block subscriber (deadlock?)")
	}
	// The Block sub admitted the new message behind the original one
	// (FIFO); the DropNew sub dropped the overflow entirely.
	if v := recvTimeout(t, blockCh, "block sub"); v != 9 {
		t.Fatalf("block sub first value = %d, want 9", v)
	}
	if v := recvTimeout(t, blockCh, "block sub"); v != 7 {
		t.Fatalf("block sub second value = %d, want 7", v)
	}
	assertEmpty(t, blockCh, "block sub")
	if v := recvTimeout(t, dropCh, "drop sub"); v != 9 {
		t.Fatalf("drop sub buffered value = %d, want 9", v)
	}
	assertEmpty(t, dropCh, "drop sub")
}

// ---------------------------------------------------------------------------
// 5. Close semantics
// ---------------------------------------------------------------------------

func TestCloseSemantics(t *testing.T) {
	b := New[int]()
	ch1, unsub1 := b.Subscribe(4)
	ch2, _ := b.Subscribe(4)

	if got := b.Publish(42); got != 2 {
		t.Fatalf("Publish delivered %d, want 2", got)
	}
	b.Close()

	if got := b.Publish(43); got != 0 {
		t.Fatalf("Publish after Close delivered %d, want 0", got)
	}

	// Buffered values remain readable, then the channel reports closed.
	if v := recvTimeout(t, ch1, "ch1"); v != 42 {
		t.Fatalf("ch1 buffered value = %d, want 42", v)
	}
	if _, ok := <-ch1; ok {
		t.Fatal("ch1 should report closed after drain")
	}
	if v := recvTimeout(t, ch2, "ch2"); v != 42 {
		t.Fatalf("ch2 buffered value = %d, want 42", v)
	}
	if _, ok := <-ch2; ok {
		t.Fatal("ch2 should report closed after drain")
	}

	b.Close() // double-Close must be a safe no-op
	unsub1()  // Unsubscribe after Close must be a safe no-op
	unsub1()  // and idempotent
	assertEmpty(t, ch1, "post-close")
}

func TestUnsubscribeIsIdempotentAndRaceFree(t *testing.T) {
	b := New[int]()
	defer b.Close()

	ch, unsub := b.Subscribe(8)
	if got := b.Publish(1); got != 1 {
		t.Fatalf("Publish delivered %d, want 1", got)
	}

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsub()
			unsub()
		}()
	}
	wg.Wait()

	if got := b.Publish(2); got != 0 {
		t.Fatalf("Publish after unsubscribe delivered %d, want 0", got)
	}
	if v := recvTimeout(t, ch, "unsub"); v != 1 {
		t.Fatalf("buffered value = %d, want 1", v)
	}
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after unsubscribe")
	}
}

func TestSubscribeCapacityClampedToMinimumOne(t *testing.T) {
	b := New[int]()
	defer b.Close()

	ch, unsub := b.Subscribe(0)
	defer unsub()
	if got := b.Publish(7); got != 1 {
		t.Fatalf("Publish to clamped sub delivered %d, want 1", got)
	}
	if v := recvTimeout(t, ch, "clamped"); v != 7 {
		t.Fatalf("got %d, want 7", v)
	}
}

// ---------------------------------------------------------------------------
// 6. generics: instantiate with concrete struct AND interface{} types
// ---------------------------------------------------------------------------

func TestGenericInstantiationWithStructType(t *testing.T) {
	b := New[point]()
	defer b.Close()

	ch, unsub := b.Subscribe(2)
	defer unsub()
	if got := b.Publish(point{X: 1, Y: 2}); got != 1 {
		t.Fatalf("Publish delivered %d, want 1", got)
	}
	if got := recvTimeout(t, ch, "point broker"); (got != point{X: 1, Y: 2}) {
		t.Fatalf("got %+v, want {1 2}", got)
	}
}

func TestGenericInstantiationWithInterfaceType(t *testing.T) {
	b := New[any]() // heterogeneous payloads through interface{}
	defer b.Close()

	ch, unsub := b.Subscribe(4)
	defer unsub()
	payloads := []any{1, "two", point{3, 4}, nil}
	for _, p := range payloads {
		if got := b.Publish(p); got != 1 {
			t.Fatalf("Publish(%v) delivered %d, want 1", p, got)
		}
	}
	for i, want := range payloads {
		got := recvTimeout(t, ch, "any broker")
		if got != want {
			t.Fatalf("payload %d: got %v, want %v", i, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// 7. hammer test: mixed Subscribe/Publish/Unsubscribe/Close under -race
// ---------------------------------------------------------------------------

func TestHammerNoPanicNoDeadlock(t *testing.T) {
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		hammerBody()
	}()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("hammer test deadlocked")
	}
}

func hammerBody() {
	deadline := time.Now().Add(200 * time.Millisecond)
	b := New[int]()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				switch (g + i) % 5 {
				case 0:
					ch, unsub := b.Subscribe(1 + i%8)
					time.Sleep(time.Millisecond)
					unsub()
					_, _ = <-ch // draining a possibly-closed channel is fine
				case 1:
					b.Publish(i)
				case 2:
					ch, unsub := b.Subscribe(2)
					select {
					case <-ch:
					default:
					}
					unsub()
				case 3:
					ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
					b.PublishBlocking(ctx, i)
					cancel()
				case 4:
					if i%16 == 4 {
						b.Close() // repeated closes are safe by contract
					} else {
						b.Publish(i)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	b.Close()
}
