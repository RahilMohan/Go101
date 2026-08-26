// Package tagforge validates config structs via `validate:"..."` tags.
//
// It teaches struct-tag parsing, reflection mechanics (types, values,
// fields, zero checks), the cost of reflection — and how a precompiled
// schema turns per-call tag parsing into one-time work — plus when NOT
// to reflect at all.
//
// # Tag grammar
//
// The `validate` tag holds constraints separated by spaces or commas:
//
//	required          non-zero value demanded
//	min=N             numeric >= N; string/map/slice len >= N
//	max=N             numeric <= N; len <= N
//	email             exactly one @ with non-empty local/domain parts
//	oneof=a|b|c       string membership
//	nonzero           not the zero value for its type
//
// Unknown constraint names are errors naming the field and the
// offending token (fail-closed).
package tagforge

import (
	"errors"
	"reflect"
)

// FieldError is a single violation, reported with the dotted field
// path where it occurred (e.g. "Server.Timeout").
type FieldError struct {
	Path string
	Msg  string
}

func (fe FieldError) String() string {
	return fe.Path + ": " + fe.Msg
}

// ValidationError aggregates every violation found in one pass.
type ValidationError []FieldError

func (ve ValidationError) Error() string {
	// TODO: Implement
	return "tagforge: not implemented"
}

// rawConstraint is one parsed token of a validate tag.
type rawConstraint struct {
	name string // e.g. "min"
	arg  string // e.g. "3" ("" for flag-style constraints)
}

// parseTag splits a validate tag into constraints. Unknown names,
// missing arguments, and unexpected arguments are errors.
func parseTag(tag string) ([]rawConstraint, error) {
	// TODO: Implement
	return nil, errors.New("tagforge: not implemented")
}

// Validate walks v (a struct or pointer to struct) and enforces its
// tags, returning an aggregated error listing ALL violations, each
// wrapped with its field path ("Server.Timeout: must be positive").
// Nested structs are followed recursively with dotted paths; embedded
// structs are promoted. Unexported fields are ignored silently.
func Validate(v any) error {
	// TODO: Implement
	return errors.New("tagforge: not implemented")
}

// Schema is the precompiled validation plan for one type: tags are
// parsed and checked ONCE at BuildSchema time, so Validate runs
// without re-parsing.
type Schema struct{}

// BuildSchema parses and validates all validate tags reachable from t
// (which must be a struct or pointer-to-struct type), erroring on
// unknown types or constraints.
func BuildSchema(t reflect.Type) (*Schema, error) {
	// TODO: Implement
	return nil, errors.New("tagforge: not implemented")
}

// Validate executes the precompiled plan against v without re-parsing
// any tags.
func (sc *Schema) Validate(v any) error {
	// TODO: Implement
	return errors.New("tagforge: not implemented")
}
