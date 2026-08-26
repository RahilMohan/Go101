// Package taskline provides a TODO manager that persists tasks as a JSON
// array in a single file, teaching structs, JSON tags, slices-vs-maps, and
// atomic file I/O.
package taskline

import (
	"errors"
	"time"
)

// Status is the lifecycle state of a Task. Transitions are free — there is no
// state-machine validation.
type Status string

const (
	StatusTodo  Status = "todo"
	StatusDoing Status = "doing"
	StatusDone  Status = "done"
)

// Task is a single TODO item.
type Task struct {
	ID        int        `json:"id"`
	Title     string     `json:"title"`
	Status    Status     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	DoneAt    *time.Time `json:"done_at,omitempty"` // nil unless StatusDone
}

// ErrNotFound is returned when an operation references an unknown task ID.
var ErrNotFound = errors.New("taskline: task not found")

// ErrNullElement is returned by List when the store file is a JSON array
// containing null elements, which would otherwise decode into phantom
// zero-value tasks.
var ErrNullElement = errors.New("taskline: store contains null array element")

// Store persists tasks as a JSON array in a single file. Concurrent
// multi-process use is last-writer-wins; a single writer per file is
// assumed.
type Store struct {
	path string // TODO: Implement
}

// OpenStore returns a Store backed by the JSON file at path. The file need
// not exist yet; it is created on the first mutation.
func OpenStore(path string) (*Store, error) {
	// TODO: Implement
	return nil, nil
}

// List returns all tasks. A missing or empty file yields an empty (non-nil)
// slice. A corrupt file yields an error wrapping the decode failure.
func (s *Store) List() ([]Task, error) {
	// TODO: Implement
	return nil, nil
}

// Add appends a new task with Title title. Its ID is max(existing)+1 (1 when
// empty), its status is StatusTodo and CreatedAt is now.
func (s *Store) Add(title string) (Task, error) {
	// TODO: Implement
	return Task{}, nil
}

// SetStatus sets the status of the task with ID id. It returns an error
// wrapping ErrNotFound if the ID is absent. DoneAt is set when st is
// StatusDone and cleared otherwise.
func (s *Store) SetStatus(id int, st Status) error {
	// TODO: Implement
	return nil
}

// Remove deletes the task with ID id. It returns an error wrapping
// ErrNotFound if the ID is absent.
func (s *Store) Remove(id int) error {
	// TODO: Implement
	return nil
}
