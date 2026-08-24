package taskline

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mustOpen opens a store at a unique path inside the test's temp dir and
// returns both, so tests can reopen or inspect the raw file.
func mustOpen(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.json")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore(%q) error = %v", path, err)
	}
	return s, path
}

func TestAddListRoundtrip(t *testing.T) {
	tests := []struct {
		name   string
		titles []string
	}{
		{"single task", []string{"buy milk"}},
		{"multiple tasks get increasing IDs", []string{"first", "second", "third"}},
		{"unicode title", []string{"fika ☕ med kanelbulle"}},
		{"title with spaces", []string{"water the office plants"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := mustOpen(t)

			var prev time.Time
			for i, title := range tt.titles {
				task, err := s.Add(title)
				if err != nil {
					t.Fatalf("Add(%q) error = %v", title, err)
				}
				if task.ID != i+1 {
					t.Errorf("Add(%q) ID = %d, want %d", title, task.ID, i+1)
				}
				if task.Title != title {
					t.Errorf("Add(%q) Title = %q", title, task.Title)
				}
				if task.Status != StatusTodo {
					t.Errorf("Add(%q) Status = %q, want %q", title, task.Status, StatusTodo)
				}
				if task.CreatedAt.IsZero() {
					t.Errorf("Add(%q) CreatedAt is zero", title)
				}
				if task.CreatedAt.Before(prev) {
					t.Errorf("Add(%q) CreatedAt %v before previous %v (not monotonic)", title, task.CreatedAt, prev)
				}
				prev = task.CreatedAt
			}

			got, err := s.List()
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(got) != len(tt.titles) {
				t.Fatalf("List() len = %d, want %d", len(got), len(tt.titles))
			}
			for i, g := range got {
				want := Task{ID: i + 1, Title: tt.titles[i], Status: StatusTodo, CreatedAt: g.CreatedAt}
				if g != want {
					t.Errorf("List()[%d] = %+v, want %+v", i, g, want)
				}
			}
		})
	}
}

func TestListMissingFileReturnsEmptyNonNilSlice(t *testing.T) {
	s, _ := mustOpen(t) // never mutated, file does not exist
	got, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got == nil {
		t.Fatal("List() returned nil slice, want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("List() len = %d, want 0", len(got))
	}
}

func TestListEmptyFileReturnsEmptyNonNilSlice(t *testing.T) {
	s, path := mustOpen(t)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("List() = %#v, want empty non-nil slice", got)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	s, path := mustOpen(t)
	first, err := s.Add("persist me")
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if _, err := s.Add("and me"); err != nil {
		t.Fatalf("Add error = %v", err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore(reopen) error = %v", err)
	}
	got, err := reopened.List()
	if err != nil {
		t.Fatalf("List() after reopen error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after reopen List() len = %d, want 2", len(got))
	}
	if got[0].ID != first.ID || got[0].Title != first.Title || !got[0].CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("after reopen got[0] = %+v, want %+v", got[0], first)
	}
}

func TestSetStatusRewritesFileAtomicallyAndParses(t *testing.T) {
	s, path := mustOpen(t)
	if _, err := s.Add("shape check"); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if err := s.SetStatus(1, StatusDoing); err != nil {
		t.Fatalf("SetStatus error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatalf("file does not parse as JSON after SetStatus: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != StatusDoing {
		t.Errorf("file contains %+v, want one task with status %q", tasks, StatusDoing)
	}
}

func TestErrNotFoundOnMissingID(t *testing.T) {
	s, _ := mustOpen(t)
	if _, err := s.Add("only"); err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if err := s.SetStatus(99, StatusDone); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetStatus(99) error = %v, want errors.Is(err, ErrNotFound)", err)
	}
	if err := s.Remove(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove(99) error = %v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestRemoveDeletesTask(t *testing.T) {
	s, _ := mustOpen(t)
	for _, title := range []string{"keep", "drop"} {
		if _, err := s.Add(title); err != nil {
			t.Fatalf("Add(%q) error = %v", title, err)
		}
	}
	if err := s.Remove(1); err != nil {
		t.Fatalf("Remove(1) error = %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != 2 {
		t.Errorf("after Remove(1) List() = %+v, want only task ID 2", got)
	}

	// IDs are max(existing)+1, so after removing ID 1 the next Add gets 3.
	task, err := s.Add("next")
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if task.ID != 3 {
		t.Errorf("Add after Remove ID = %d, want 3 (max existing + 1)", task.ID)
	}
}

func TestCorruptFileErrorsOnList(t *testing.T) {
	s, path := mustOpen(t)
	if err := os.WriteFile(path, []byte("{not json at all"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List() on corrupt file error = nil, want decode error")
	}
}

func TestDoneAtLifecycle(t *testing.T) {
	s, _ := mustOpen(t)
	task, err := s.Add("finish report")
	if err != nil {
		t.Fatalf("Add error = %v", err)
	}
	if task.DoneAt != nil {
		t.Errorf("freshly added task DoneAt = %v, want nil", task.DoneAt)
	}

	if err := s.SetStatus(1, StatusDone); err != nil {
		t.Fatalf("SetStatus(done) error = %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() = %+v, want one task", got)
	}
	if got[0].DoneAt == nil {
		t.Error("DoneAt = nil after SetStatus(done), want non-nil")
	} else if got[0].DoneAt.Before(got[0].CreatedAt) {
		t.Errorf("DoneAt %v before CreatedAt %v", *got[0].DoneAt, got[0].CreatedAt)
	}

	if err := s.SetStatus(1, StatusDoing); err != nil {
		t.Fatalf("SetStatus(doing) error = %v", err)
	}
	got, err = s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() = %+v, want one task", got)
	}
	if got[0].DoneAt != nil {
		t.Errorf("DoneAt = %v after transitioning away from done, want nil", got[0].DoneAt)
	}
}
