package solution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustAdd(t *testing.T, g *Graph, task *Task) {
	t.Helper()
	if err := g.Add(task); err != nil {
		t.Fatalf("Add(%q): unexpected error: %v", task.ID, err)
	}
}

// store is a shared map that task Run funcs write their results into.
// Because the orchestrator must only start a task after its deps
// completed, entries written by dep tasks are guaranteed visible to
// dependent tasks — this is how the tests verify "results only from
// completed deps".
type store struct {
	mu   sync.Mutex
	vals map[string]any
}

func newStore() *store { return &store{vals: map[string]any{}} }

func (s *store) put(id string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vals[id] = v
}

func (s *store) get(id string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.vals[id]
	return v, ok
}

// blockingRun returns a Run func that records it started, then blocks
// until ctx is done.
func blockingRun(started *sync.WaitGroup) func(ctx context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		started.Done()
		<-ctx.Done()
		return nil, nil
	}
}

func TestLinearChain(t *testing.T) {
	g := New()
	st := newStore()

	mustAdd(t, g, &Task{ID: "a", Run: func(ctx context.Context) (any, error) {
		st.put("a", 1)
		return 1, nil
	}})
	mustAdd(t, g, &Task{ID: "b", Deps: []string{"a"}, Run: func(ctx context.Context) (any, error) {
		if _, ok := st.get("a"); !ok {
			return nil, errors.New("b ran before a published")
		}
		st.put("b", 2)
		return 2, nil
	}})
	mustAdd(t, g, &Task{ID: "c", Deps: []string{"b"}, Run: func(ctx context.Context) (any, error) {
		if _, ok := st.get("b"); !ok {
			return nil, errors.New("c ran before b published")
		}
		return 3, nil
	}})

	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v, ok := Get[int](res, "c"); !ok || v != 3 {
		t.Fatalf(`Get[int](res, "c") = %d, %v; want 3, true`, v, ok)
	}
}

func TestDiamondFanOutFanIn(t *testing.T) {
	g := New()
	st := newStore()

	mustAdd(t, g, &Task{ID: "a", Run: func(ctx context.Context) (any, error) {
		st.put("a", 10)
		return 10, nil
	}})
	for _, id := range []string{"b", "c"} {
		id := id
		mustAdd(t, g, &Task{ID: id, Deps: []string{"a"}, Run: func(ctx context.Context) (any, error) {
			v, ok := st.get("a")
			if !ok || v.(int) != 10 {
				return nil, fmt.Errorf("%s: dep a value not visible at execution time", id)
			}
			st.put(id, 11)
			return 11, nil
		}})
	}
	mustAdd(t, g, &Task{ID: "d", Deps: []string{"b", "c"}, Run: func(ctx context.Context) (any, error) {
		bv, bok := st.get("b")
		cv, cok := st.get("c")
		if !bok || !cok {
			return nil, errors.New("d: fan-in deps not visible")
		}
		return bv.(int) + cv.(int), nil
	}})

	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v, ok := Get[int](res, "d"); !ok || v != 22 {
		t.Fatalf(`Get[int](res, "d") = %d, %v; want 22, true`, v, ok)
	}
}

func TestWideParallelOrderIndependent(t *testing.T) {
	const n = 50
	g := New()
	for i := 0; i < n; i++ {
		i := i
		mustAdd(t, g, &Task{ID: fmt.Sprintf("w%02d", i), Run: func(ctx context.Context) (any, error) {
			return i * i, nil
		}})
	}

	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("w%02d", i)
		v, ok := Get[int](res, id)
		if !ok || v != i*i {
			t.Fatalf("Get[int](res, %q) = %d, %v; want %d, true", id, v, ok, i*i)
		}
	}
}

func TestAddErrors(t *testing.T) {
	g := New()
	noop := func(ctx context.Context) (any, error) { return nil, nil }
	mustAdd(t, g, &Task{ID: "x", Run: noop})

	if err := g.Add(&Task{ID: "x", Run: noop}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate Add error = %v; want errors.Is ErrDuplicate", err)
	}
	if err := g.Add(&Task{ID: "", Run: noop}); err == nil {
		t.Fatal("empty-ID Add: want error, got nil")
	}
	if err := g.Add(nil); err == nil {
		t.Fatal("nil-task Add: want error, got nil")
	}
}

func TestValidateUnknownDep(t *testing.T) {
	g := New()
	mustAdd(t, g, &Task{ID: "a", Deps: []string{"ghost"}, Run: func(ctx context.Context) (any, error) {
		return nil, nil
	}})

	if err := g.Validate(); !errors.Is(err, ErrUnknownDep) {
		t.Fatalf("Validate error = %v; want errors.Is ErrUnknownDep", err)
	}
	if _, err := g.Run(context.Background()); !errors.Is(err, ErrUnknownDep) {
		t.Fatalf("Run on invalid graph error = %v; want errors.Is ErrUnknownDep", err)
	}
}

func TestValidateCycleNamesMembers(t *testing.T) {
	g := New()
	noop := func(ctx context.Context) (any, error) { return nil, nil }
	mustAdd(t, g, &Task{ID: "a", Deps: []string{"c"}, Run: noop})
	mustAdd(t, g, &Task{ID: "b", Deps: []string{"a"}, Run: noop})
	mustAdd(t, g, &Task{ID: "c", Deps: []string{"b"}, Run: noop})

	err := g.Validate()
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("Validate error = %v; want errors.Is ErrCycle", err)
	}
	for _, member := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), member) {
			t.Fatalf("cycle error %q does not name chain member %q", err.Error(), member)
		}
	}
}

func TestSelfCycle(t *testing.T) {
	g := New()
	mustAdd(t, g, &Task{ID: "self", Deps: []string{"self"}, Run: func(ctx context.Context) (any, error) {
		return nil, nil
	}})
	if err := g.Validate(); !errors.Is(err, ErrCycle) {
		t.Fatalf("Validate self-dep error = %v; want errors.Is ErrCycle", err)
	}
}

func TestFirstTaskErrorCancelsSiblingsAndWraps(t *testing.T) {
	sentinel := errors.New("boom")
	g := New()
	siblingStarted := make(chan struct{})
	failNow := make(chan struct{})
	mustAdd(t, g, &Task{ID: "sibling", Run: func(ctx context.Context) (any, error) {
		close(siblingStarted)
		<-ctx.Done()
		return nil, nil
	}})
	mustAdd(t, g, &Task{ID: "bad", Run: func(ctx context.Context) (any, error) {
		<-failNow
		return nil, sentinel
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	var res *Results
	var err error
	go func() {
		defer close(done)
		res, err = g.Run(ctx)
	}()

	select { // sibling is in flight and blocking on ctx
	case <-siblingStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("sibling task never started")
	}
	close(failNow) // now let "bad" fail; siblings must be cancelled
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of task failure")
	}

	if err == nil {
		t.Fatal("Run: want error from failing task, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v; want wrap of %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Fatalf(`Run error %q does not name failing task "bad"`, err.Error())
	}
	if res != nil {
		t.Fatalf("Run returned non-nil Results on failure: %v", res)
	}
}

func TestCancellationPropagationBoundedTime(t *testing.T) {
	g := New()
	var started sync.WaitGroup
	started.Add(2)
	mustAdd(t, g, &Task{ID: "slow1", Run: blockingRun(&started)})
	mustAdd(t, g, &Task{ID: "slow2", Run: blockingRun(&started)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		started.Wait()
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	begin := time.Now()
	res, err := g.Run(ctx)
	elapsed := time.Since(begin)

	// Bounded wall-clock assertion per master plan Phase 3: cancellation
	// must propagate through scheduling AND in-flight tasks within ~5s,
	// enforced by timer comparison — never by hope.
	const budget = 5 * time.Second
	if elapsed >= budget {
		t.Fatalf("Run took %v after cancellation; want return within %v", elapsed, budget)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v; want context.Canceled", err)
	}
	if _, ok := Get[int](res, "slow1"); ok {
		t.Fatal("expected no typed results after cancelled run")
	}
}

func TestGetTypedRetrieval(t *testing.T) {
	g := New()
	mustAdd(t, g, &Task{ID: "num", Run: func(ctx context.Context) (any, error) { return 42, nil }})
	mustAdd(t, g, &Task{ID: "text", Run: func(ctx context.Context) (any, error) { return "hello", nil }})

	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if v, ok := Get[int](res, "num"); !ok || v != 42 {
		t.Fatalf("Get[int] num = %d, %v; want 42, true", v, ok)
	}
	if v, ok := Get[string](res, "text"); !ok || v != "hello" {
		t.Fatalf("Get[string] text = %q, %v; want hello, true", v, ok)
	}
	// Wrong type yields zero value, false — never panics.
	if v, ok := Get[string](res, "num"); ok || v != "" {
		t.Fatalf("Get[string] num = %q, %v; want zero, false", v, ok)
	}
	// Missing ID.
	if v, ok := Get[int](res, "nope"); ok || v != 0 {
		t.Fatalf("Get missing = %d, %v; want zero, false", v, ok)
	}
	// Nil Results is safe.
	var nilRes *Results
	if v, ok := Get[int](nilRes, "num"); ok || v != 0 {
		t.Fatalf("Get on nil Results = %d, %v; want zero, false", v, ok)
	}
}

func TestEmptyGraph(t *testing.T) {
	g := New()
	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("Run empty graph: %v", err)
	}
	if res == nil {
		t.Fatal("Run empty graph: want non-nil Results")
	}
}

func TestConcurrencyStress(t *testing.T) {
	const n = 200
	g := New()
	var concurrent, maxConcurrent int64
	for i := 0; i < n; i++ {
		i := i
		mustAdd(t, g, &Task{ID: fmt.Sprintf("s%03d", i), Run: func(ctx context.Context) (any, error) {
			cur := atomic.AddInt64(&concurrent, 1)
			defer atomic.AddInt64(&concurrent, -1)
			for {
				old := atomic.LoadInt64(&maxConcurrent)
				if cur <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			return i, nil
		}})
	}

	res, err := g.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("s%03d", i)
		if v, ok := Get[int](res, id); !ok || v != i {
			t.Fatalf("Get[%q] = %d, %v; want %d, true", id, v, ok, i)
		}
	}
	if m := atomic.LoadInt64(&maxConcurrent); m < 2 {
		t.Fatalf("max observed concurrency = %d; want parallel execution (>= 2)", m)
	}
}

func TestResultsOnlyFromCompletedDeps(t *testing.T) {
	g := New()
	st := newStore()
	release := make(chan struct{})

	mustAdd(t, g, &Task{ID: "gate", Run: func(ctx context.Context) (any, error) {
		<-release
		st.put("gate", 7)
		return 7, nil
	}})
	mustAdd(t, g, &Task{ID: "consumer", Deps: []string{"gate"}, Run: func(ctx context.Context) (any, error) {
		v, ok := st.get("gate")
		if !ok {
			return nil, errors.New("consumer ran before gate result published")
		}
		return v.(int), nil
	}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := g.Run(context.Background()); err != nil {
			t.Errorf("Run: %v", err)
		}
	}()

	select {
	case <-done:
		t.Fatal("consumer ran despite blocked dependency")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-done
}
