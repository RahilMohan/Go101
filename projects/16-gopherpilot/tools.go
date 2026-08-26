package gopherpilot

import (
	"context"
	"errors"
)

// ErrPathEscape is returned when a model-supplied path resolves outside the
// workspace root (traversal guard).
var ErrPathEscape = errors.New("gopherpilot: path escapes workspace")

// Workspace confines every file operation to a sandbox root directory.
type Workspace struct {
	Root string // absolute
}

// NewWorkspace creates (if needed) and pins the sandbox root.
func NewWorkspace(root string) (*Workspace, error) {
	// TODO: filepath.Abs + MkdirAll.
	return nil, nil
}

// Resolve maps a relative path to an absolute path inside Root, rejecting
// absolute inputs and any traversal that escapes the workspace.
func (w *Workspace) Resolve(rel string) (string, error) {
	// TODO: clean + prefix check; wrap failures with %w of ErrPathEscape.
	return "", nil
}

// ReadFile returns the contents of rel within the sandbox.
func (w *Workspace) ReadFile(rel string) (string, error) {
	// TODO: resolve then read.
	return "", nil
}

// WriteFile writes content to rel within the sandbox.
func (w *Workspace) WriteFile(rel, content string) error {
	// TODO: resolve then write.
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
	// TODO: build set.
	return nil
}

// Execute runs name with args in the workspace dir, returning combined output.
func (r *Runner) Execute(ctx context.Context, name string, args ...string) (string, error) {
	// TODO: allowlist check + exec.CommandContext with Dir = WS.Root.
	return "", nil
}
