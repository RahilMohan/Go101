package gopherpilot

import (
	"context"
	"errors"
)

// Agent states. The only legal path is:
// Idle → Thinking → (ToolCall → Executing)* → Thinking* → Done.
type State string

const (
	StateIdle      State = "idle"
	StateThinking  State = "thinking"
	StateToolCall  State = "tool_call"
	StateExecuting State = "executing"
	StateDone      State = "done"
)

// ErrInvalidTransition is returned by Transition for any edge not in the
// legal state graph.
var ErrInvalidTransition = errors.New("gopherpilot: invalid state transition")

// ErrTooManyRounds aborts runaway tool loops.
var ErrTooManyRounds = errors.New("gopherpilot: exceeded max tool rounds")

// MaxToolRounds bounds the number of provider round-trips in one Run.
const MaxToolRounds = 8

// validTransitions is the explicit state graph.
var validTransitions = map[State][]State{
	StateIdle:      {StateThinking},
	StateThinking:  {StateToolCall, StateDone},
	StateToolCall:  {StateExecuting},
	StateExecuting: {StateThinking},
}

// Agent is the tool-execution loop: it streams from a Provider, executes
// requested tools sandboxed in a Workspace, wraps results as delimited data
// blocks, and feeds them back until the model emits a final answer.
type Agent struct {
	prov   Provider
	ws     *Workspace
	runner *Runner
	st     State
	log    []State
}

// NewAgent wires the loop together; the agent starts Idle.
func NewAgent(p Provider, ws *Workspace, r *Runner) *Agent {
	// TODO: init with StateIdle history.
	return nil
}

// Transition moves to the next state or returns an error wrapping
// ErrInvalidTransition naming both states.
func (a *Agent) Transition(to State) error {
	// TODO: validate against validTransitions; record on success.
	return nil
}

// State reports the current state.
func (a *Agent) State() State {
	// TODO: return current state.
	return ""
}

// Transitions returns the recorded transition history (for tests/specs).
func (a *Agent) Transitions() []State {
	// TODO: defensive copy.
	return nil
}

// Run drives the full loop for task and returns the final answer text.
func (a *Agent) Run(ctx context.Context, task string) (string, error) {
	// TODO: implement loop per spec.
	return "", nil
}

// collectEvents drains ch into a slice via a goroutine, honouring ctx.
func collectEvents(ctx context.Context, ch <-chan StreamEvent) ([]StreamEvent, error) {
	// TODO: goroutine + select on ctx.Done().
	return nil, nil
}

// execTool dispatches one ToolCall to workspace/runner implementations.
func execTool(ctx context.Context, ws *Workspace, r *Runner, call ToolCall) (string, error) {
	// TODO: read_file / write_file / run_cmd dispatch.
	return "", nil
}

// WrapToolOutput renders a tool result as a clearly delimited data-only
// block so injected instructions inside output stay inert.
func WrapToolOutput(tool string, output string) string {
	// TODO: delimit + neutralize embedded closing tags.
	return ""
}
