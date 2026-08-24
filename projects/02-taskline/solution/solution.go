// Package taskline provides a TODO manager that persists tasks as a JSON
// array in a single file, teaching structs, JSON tags, slices-vs-maps, and
// atomic file I/O.
package solution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// Store persists tasks as a JSON array in a single file. Every mutation
// rewrites the whole file atomically: the new content is written to a temp
// file in the same directory, flushed, and renamed over the target, so a
// crash mid-write never leaves a truncated or half-updated store behind.
type Store struct {
	path string
}

// OpenStore returns a Store backed by the JSON file at path. The file need
// not exist yet; it is created on the first mutation.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("taskline: empty store path")
	}
	return &Store{path: path}, nil
}

// List returns all tasks. A missing or empty file yields an empty (non-nil)
// slice. A corrupt file yields an error wrapping the decode failure — it
// never panics and never silently drops data.
func (s *Store) List() ([]Task, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return []Task{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("taskline: read %s: %w", s.path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return []Task{}, nil
	}
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("taskline: decode %s: %w", s.path, err)
	}
	if tasks == nil {
		tasks = []Task{}
	}
	return tasks, nil
}

// Add appends a new task with Title title. Its ID is max(existing)+1 (1 when
// empty), its status is StatusTodo and CreatedAt is now.
func (s *Store) Add(title string) (Task, error) {
	tasks, err := s.List()
	if err != nil {
		return Task{}, fmt.Errorf("taskline: add: %w", err)
	}
	max := 0
	for _, t := range tasks {
		if t.ID > max {
			max = t.ID
		}
	}
	task := Task{
		ID:        max + 1,
		Title:     title,
		Status:    StatusTodo,
		CreatedAt: time.Now(),
	}
	if err := s.save(append(tasks, task)); err != nil {
		return Task{}, fmt.Errorf("taskline: add: %w", err)
	}
	return task, nil
}

// SetStatus sets the status of the task with ID id. It returns an error
// wrapping ErrNotFound if the ID is absent. DoneAt is set when st is
// StatusDone and cleared otherwise, keeping the field consistent with Status.
func (s *Store) SetStatus(id int, st Status) error {
	tasks, err := s.List()
	if err != nil {
		return fmt.Errorf("taskline: set status: %w", err)
	}
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Status = st
			if st == StatusDone {
				now := time.Now()
				tasks[i].DoneAt = &now
			} else {
				tasks[i].DoneAt = nil
			}
			return s.save(tasks)
		}
	}
	return fmt.Errorf("taskline: set status: %w", ErrNotFound)
}

// Remove deletes the task with ID id. It returns an error wrapping
// ErrNotFound if the ID is absent.
func (s *Store) Remove(id int) error {
	tasks, err := s.List()
	if err != nil {
		return fmt.Errorf("taskline: remove: %w", err)
	}
	for i := range tasks {
		if tasks[i].ID == id {
			tasks = append(tasks[:i], tasks[i+1:]...)
			return s.save(tasks)
		}
	}
	return fmt.Errorf("taskline: remove: %w", ErrNotFound)
}

// save rewrites the store file atomically: write to a temp file in the same
// directory (so rename stays within one filesystem), sync it, then rename it
// over the target path.
func (s *Store) save(tasks []Task) error {
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".taskline-*.tmp")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	name := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		os.Remove(name)
		return fmt.Errorf("rename over %s: %w", s.path, err)
	}
	return nil
}
