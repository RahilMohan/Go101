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
	// rootEval is Root with all symlinks resolved; used so confinement
	// checks compare consistently evaluated paths (macOS /tmp-style links).
	rootEval string
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
	ws := &Workspace{Root: abs}
	if ev, err := filepath.EvalSymlinks(abs); err == nil {
		ws.rootEval = ev
	} else {
		ws.rootEval = abs
	}
	return ws, nil
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

// evalDeepestExisting resolves symlinks for the deepest existing ancestor of
// path and re-joins the not-yet-existing tail lexically, so paths that do not
// exist yet (write targets) can still be confinement-checked.
func evalDeepestExisting(path string) (string, error) {
	p := path
	var tail []string // path components below the existing prefix, reversed
	for {
		ev, err := filepath.EvalSymlinks(p)
		if err == nil {
			out := ev
			for i := len(tail) - 1; i >= 0; i-- {
				out = filepath.Join(out, tail[i])
			}
			return out, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		dir, file := filepath.Split(p)
		if dir == "" || dir == p {
			return "", err // reached the filesystem root without finding it
		}
		tail = append(tail, file)
		p = filepath.Clean(dir)
	}
}

// confine re-checks an already lexically-resolved path against symlink
// resolution: a link inside the workspace pointing outside the root must be
// rejected even though the lexical Clean/Join check passed.
func (w *Workspace) confine(full string) (string, error) {
	root := w.rootEval
	if root == "" {
		root = w.Root
	}
	ev, err := evalDeepestExisting(full)
	if err != nil {
		return "", fmt.Errorf("%w: cannot safely resolve %q: %v", ErrPathEscape, full, err)
	}
	if ev != root && !strings.HasPrefix(ev, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q escapes %s via symlink", ErrPathEscape, full, root)
	}
	return ev, nil
}

// ReadFile returns the contents of rel within the sandbox.
func (w *Workspace) ReadFile(rel string) (string, error) {
	full, err := w.Resolve(rel)
	if err != nil {
		return "", err
	}
	if full, err = w.confine(full); err != nil {
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
	if full, err = w.confine(full); err != nil {
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
	// Minimal explicit environment: never inherit the parent env, or any
	// allowlisted binary could print GOPHERPILOT_API_KEY into tool output
	// that is fed straight back to the model.
	env := make([]string, 0, 3)
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("gopherpilot: run %q: %w", name, err)
	}
	return string(out), nil
}
