// Package pipeline provides a DAG workflow orchestrator: tasks with
// dependencies are validated for unknown references and cycles, then
// executed concurrently — every task whose dependencies are complete is
// scheduled immediately, bounded by an internal worker limit. It teaches
// generics (typed result retrieval), errgroup-based structured
// concurrency, cancellation propagation, and deadlock avoidance.
package pipeline

import (
	"context"
	"errors"
)

// Sentinel errors returned by Add, Validate, and Run. Use errors.Is to
// test for them; returned errors wrap these and name the offending task.
var (
	// ErrDuplicate is reported by Add when a task ID is already present.
	ErrDuplicate = errors.New("duplicate task")
	// ErrUnknownDep is reported by Validate when a task depends on an ID
	// that was never added.
	ErrUnknownDep = errors.New("unknown dependency")
	// ErrCycle is reported by Validate when the dependency graph contains
	// a cycle; the error message names the tasks in the chain.
	ErrCycle = errors.New("dependency cycle")
)

// Task is one node in the workflow DAG. Run receives the context passed
// to Graph.Run so long-running work can honor cancellation.
type Task struct {
	// ID uniquely identifies the task within a graph. Must be non-empty.
	ID string
	// Deps lists IDs of tasks that must complete successfully before
	// this task may run.
	Deps []string
	// Run executes the task and returns its result value (stored in
	// Results under ID) or an error. If it returns an error, sibling
	// tasks are cancelled via the derived context.
	Run func(ctx context.Context) (any, error)
}

// Graph is a directed acyclic graph of tasks. It must be constructed
// with New and populated before first use; a Graph is not safe for
// concurrent mutation.
type Graph struct {
	_ struct{}
}

// New returns an empty graph ready to accept tasks.
func New() *Graph {
	// TODO: Implement
	return nil
}

// Add registers t in the graph. It reports ErrDuplicate if the ID is
// already registered and an error if t is nil, has an empty ID, or has
// a nil Run func.
func (g *Graph) Add(t *Task) error {
	// TODO: Implement
	return nil
}

// Validate checks referential integrity and acyclicity. It wraps
// ErrUnknownDep if any task names a dependency that was never added
// (with multiple offenders, the lowest task ID is reported), and
// ErrCycle (naming the quoted chain members) if the dependency edges
// form a cycle. Task execution is panic-safe: a panicking Run func is
// reported as a wrapped error naming the task instead of crashing the
// process.
func (g *Graph) Validate() error {
	// TODO: Implement
	return nil
}

// Results holds the values produced by a successful Graph.Run. Values
// are retrieved type-safely with Get.
type Results struct {
	_ struct{}
}

// Get returns the result produced by the task with the given id, typed
// as T. The second return value is false when no such task exists, the
// run failed, or the stored value does not hold a T.
func Get[T any](r *Results, id string) (T, bool) {
	// TODO: Implement
	var zero T
	return zero, false
}

// Run executes every task in the graph once all of its dependencies
// have completed successfully, scheduling all ready tasks concurrently
// (bounded by an internal worker limit). It validates the graph first.
//
// On the first task failure, remaining scheduling stops, sibling tasks
// are cancelled through their context, in-flight tasks are awaited, and
// a wrapped error naming the failed task is returned. On ctx
// cancellation, scheduling stops, in-flight tasks are awaited, and
// ctx.Err() is returned. Run never deadlocks: even when tasks block on
// the context, it returns promptly after cancellation.
func (g *Graph) Run(ctx context.Context) (*Results, error) {
	// TODO: Implement
	return nil, nil
}
