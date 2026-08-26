package gopherpilot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrPathEscape is returned when a model-supplied path resolves outside the
// workspace root (traversal guard).
var ErrPathEscape = errors.New("gopherpilot: path escapes workspace")

// cmdTimeout bounds each sandboxed command execution.
const cmdTimeout = 10 * time.Second

// Workspace confines every file operation to a sandbox root directory.
type Workspace struct {
	Root string // absolute
}

// NewWorkspace creates (if needed) and pins the sandbox root.
func NewWorkspace(root string) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("gopherpilot: resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("gopherpilot: create workspace: %w", err)
	}
	return &Workspace{Root: abs}, nil
}

// Resolve maps a relative path to an absolute path inside Root, rejecting
// absolute inputs and any traversal that escapes the workspace.
func (w *Workspace) Resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("%w: empty path", ErrPathEscape)
	}
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: %q is absolute", ErrPathEscape, rel)
	}
	full := filepath.Join(w.Root, cleaned)
	if full != w.Root && !strings.HasPrefix(full, w.Root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q resolves outside %s", ErrPathEscape, rel, w.Root)
	}
	return full, nil
}

// ReadFile returns the contents of rel within the sandbox.
func (w *Workspace) ReadFile(rel string) (string, error) {
	full, err := w.Resolve(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("gopherpilot: read %q: %w", rel, err)
	}
	return string(data), nil
}

// WriteFile writes content to rel within the sandbox.
func (w *Workspace) WriteFile(rel, content string) error {
	full, err := w.Resolve(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("gopherpilot: mkdir for %q: %w", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return fmt.Errorf("gopherpilot: write %q: %w", rel, err)
	}
	return nil
}

// Runner executes run_cmd tool calls, allowlist-restricted and rooted at the
// workspace so commands cannot touch files outside the sandbox via cwd.
type Runner struct {
	WS      *Workspace
	Allowed map[string]bool
}

// NewRunner builds a runner permitting exactly the named binaries.
func NewRunner(ws *Workspace, allowed []string) *Runner {
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	return &Runner{WS: ws, Allowed: set}
}

// Execute runs name with args in the workspace dir, returning combined output.
func (r *Runner) Execute(ctx context.Context, name string, args ...string) (string, error) {
	if r == nil || r.WS == nil {
		return "", errors.New("gopherpilot: runner has no workspace")
	}
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("gopherpilot: invalid command name %q", name)
	}
	if !r.Allowed[name] {
		return "", fmt.Errorf("gopherpilot: command %q not in allowlist", name)
	}
	cctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Dir = r.WS.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("gopherpilot: run %q: %w", name, err)
	}
	return string(out), nil
}
