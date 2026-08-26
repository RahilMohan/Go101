// Package solution is the reference implementation of the pipeline
// project: a DAG workflow orchestrator built on errgroup structured
// concurrency.
//
// # Concurrency design
//
// Run uses three cooperating pieces:
//
//   - A single dispatcher goroutine that owns all scheduling state
//     (in-degrees, ready queue, results). Single-ownership means the
//     state machine needs no locks and cannot race.
//   - A fixed pool of worker goroutines that receive ready tasks from
//     an unbuffered channel, run them with the derived context, and
//     report completions back over a second channel. The channel handoff
//     is the semaphore: at most len(workers) tasks are in flight.
//   - errgroup.WithContext, which ties it together: any error (task
//     failure or cancellation) cancels the derived context so siblings
//     stop, while eg.Wait still waits for in-flight tasks to return —
//     no leaks, no deadlock.
//
// The deadlock-avoidance subtlety: every blocking send/receive in both
// the dispatcher and the workers selects on ctx.Done() as well. When a
// task fails mid-flight, the dispatcher may be blocked offering a ready
// task nobody will take; the context branch unblocks it.
package solution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
)

var (
	ErrDuplicate  = errors.New("duplicate task")
	ErrUnknownDep = errors.New("unknown dependency")
	ErrCycle      = errors.New("dependency cycle")
)

type Task struct {
	ID   string
	Deps []string
	Run  func(ctx context.Context) (any, error)
}

type Graph struct {
	tasks map[string]*Task
}

func New() *Graph {
	return &Graph{tasks: make(map[string]*Task)}
}

func (g *Graph) Add(t *Task) error {
	if t == nil {
		return errors.New("pipeline: cannot add nil task")
	}
	if t.ID == "" {
		return errors.New("pipeline: task ID must not be empty")
	}
	if _, exists := g.tasks[t.ID]; exists {
		return fmt.Errorf("pipeline: %w: %q", ErrDuplicate, t.ID)
	}
	cp := *t
	if t.Deps != nil {
		cp.Deps = append([]string(nil), t.Deps...)
	}
	g.tasks[t.ID] = &cp
	return nil
}

func (g *Graph) Validate() error {
	for _, t := range g.tasks {
		for _, dep := range t.Deps {
			if _, ok := g.tasks[dep]; !ok {
				return fmt.Errorf("pipeline: task %q references %w %q", t.ID, ErrUnknownDep, dep)
			}
		}
	}
	return g.detectCycles()
}

// detectCycles runs a depth-first search colored white/gray/black; when
// it re-enters a gray node it reconstructs the chain from the DFS stack
// and reports it in ErrCycle's message.
func (g *Graph) detectCycles() error {
	const (
		white = iota
		gray
		black
	)
	state := make(map[string]int, len(g.tasks))
	var stack []string

	var visit func(id string) error
	visit = func(id string) error {
		state[id] = gray
		stack = append(stack, id)
		for _, dep := range g.tasks[id].Deps {
			switch state[dep] {
			case gray:
				start := 0
				for i, s := range stack {
					if s == dep {
						start = i
						break
					}
				}
				chain := append(append([]string(nil), stack[start:]...), dep)
				return fmt.Errorf("pipeline: %w: %s", ErrCycle, strings.Join(chain, " -> "))
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = black
		return nil
	}

	// Deterministic traversal order for stable error messages.
	ids := make([]string, 0, len(g.tasks))
	for id := range g.tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if state[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}

type Results struct {
	vals map[string]any
}

func Get[T any](r *Results, id string) (T, bool) {
	var zero T
	if r == nil {
		return zero, false
	}
	v, ok := r.vals[id]
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// completion reports one finished task back to the dispatcher.
type completion struct {
	id    string
	value any
	err   error
}

const maxWorkers = 16

func (g *Graph) Run(ctx context.Context) (*Results, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}

	dependents := make(map[string][]*Task, len(g.tasks))
	indegree := make(map[string]int, len(g.tasks))
	for id, t := range g.tasks {
		indegree[id] = len(t.Deps)
		for _, dep := range t.Deps {
			dependents[dep] = append(dependents[dep], t)
		}
	}

	eg, cctx := errgroup.WithContext(ctx)
	ready := make(chan *Task)
	completions := make(chan completion)

	// Written only by the dispatcher goroutine; reading it after
	// eg.Wait() is race-free because Wait synchronizes with exit.
	results := make(map[string]any, len(g.tasks))

	// Dispatcher: sole owner of scheduling state. When all workers are
	// busy, the dispatcher multiplexes between offering the next ready
	// task and collecting completions — blocking on either alone would
	// deadlock the other side.
	eg.Go(func() error {
		queue := make([]*Task, 0, len(g.tasks))
		for id, t := range g.tasks {
			if indegree[id] == 0 {
				queue = append(queue, t)
			}
		}
		sort.Slice(queue, func(i, j int) bool { return queue[i].ID < queue[j].ID })

		completed := 0
		apply := func(ev completion) error {
			if ev.err != nil {
				return fmt.Errorf("pipeline: task %q: %w", ev.id, ev.err)
			}
			results[ev.id] = ev.value
			completed++
			for _, dependent := range dependents[ev.id] {
				indegree[dependent.ID]--
				if indegree[dependent.ID] == 0 {
					queue = append(queue, dependent)
				}
			}
			return nil
		}

		for {
			if completed == len(g.tasks) {
				close(ready)
				return nil
			}

			var (
				ev  completion
				err error
			)
			if len(queue) > 0 {
				select {
				case ready <- queue[0]:
					queue = queue[1:]
					continue
				case <-cctx.Done():
					return cctx.Err()
				case ev = <-completions:
				}
			} else {
				select {
				case ev = <-completions:
				case <-cctx.Done():
					return cctx.Err()
				}
			}
			if err = apply(ev); err != nil {
				return err
			}
		}
	})

	// Workers: bounded pool pulling from the dispatcher.
	workers := len(g.tasks)
	if workers > maxWorkers {
		workers = maxWorkers
	}
	for i := 0; i < workers; i++ {
		eg.Go(func() error {
			for {
				select {
				case <-cctx.Done():
					return nil
				default:
				}
				select {
				case t, ok := <-ready:
					if !ok {
						return nil
					}
					value, err := t.Run(cctx)
					select {
					case completions <- completion{id: t.ID, value: value, err: err}:
					case <-cctx.Done():
						return nil
					}
				case <-cctx.Done():
					return nil
				}
			}
		})
	}

	waitErr := eg.Wait()
	// Cancellation of the caller's context takes precedence: report
	// ctx.Err() even if some in-flight task also surfaced an error.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return &Results{vals: results}, nil
}
