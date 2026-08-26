// Package tagforge validates config structs via `validate:"..."` tags.
//
// It teaches struct-tag parsing, reflection mechanics (types, values,
// fields, zero checks), the cost of reflection — and how a precompiled
// schema turns per-call tag parsing into one-time work — plus when NOT
// to reflect at all.
//
// # Two execution modes
//
// Validate is the naive path: every call reflects over the type and
// re-parses every tag. BuildSchema + Schema.Validate is the compiled
// path: tags are parsed and applicability-checked once, and each
// validation run only walks precomputed field plans. On realistic
// configs the schema path is several times faster; that gap IS the
// lesson. When NOT to reflect: if your config is validated once at
// startup, the naive path is plenty — don't pay abstraction for a
// cold path.
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
// offending token (fail-closed), as are malformed arguments and
// constraints that cannot apply to the field's type.
//
// # Semantics
//
//   - All violations in one pass are aggregated into a ValidationError,
//     each wrapped with its dotted field path ("Server.Timeout").
//   - Nested structs recurse with dotted paths; embedded structs are
//     promoted so their fields validate at top-level paths.
//   - A nil pointer inside a struct is a violation iff required;
//     an optional nil pointer skips its remaining checks entirely.
//   - Unexported fields are ignored silently: reflection cannot set
//     them meaningfully, so promising to validate them would lie.
package tagforge

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
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
	if len(ve) == 0 {
		return "tagforge: no violations"
	}
	parts := make([]string, len(ve))
	for i, fe := range ve {
		parts[i] = fe.String()
	}
	return fmt.Sprintf("tagforge: %d violation(s): %s", len(ve), strings.Join(parts, "; "))
}

// ---------------------------------------------------------------------------
// Tag parsing — done on EVERY call by Validate, ONCE by BuildSchema
// ---------------------------------------------------------------------------

var knownConstraints = map[string]bool{
	"required": true,
	"min":      true,
	"max":      true,
	"email":    true,
	"oneof":    true,
	"nonzero":  true,
}

// rawConstraint is one parsed token of a validate tag.
type rawConstraint struct {
	name string // e.g. "min"
	arg  string // e.g. "3" ("" for flag-style constraints)
}

// parseTag splits a validate tag into constraints (constraints may be
// separated by spaces or commas). Unknown names, missing arguments,
// and unexpected arguments are errors. It never panics on arbitrary
// input: anything it cannot make sense of is an error (see
// FuzzTagGrammar).
func parseTag(tag string) ([]rawConstraint, error) {
	fields := strings.Fields(strings.ReplaceAll(tag, ",", " "))
	if len(fields) == 0 {
		return nil, nil
	}
	out := make([]rawConstraint, 0, len(fields))
	for _, tok := range fields {
		name, arg, hasArg := strings.Cut(tok, "=")
		if !knownConstraints[name] {
			return nil, fmt.Errorf("unknown constraint %q", tok)
		}
		switch name {
		case "min", "max", "oneof":
			if !hasArg || arg == "" {
				return nil, fmt.Errorf("constraint %q requires a value (%s=...)", name, name)
			}
		default:
			if hasArg {
				return nil, fmt.Errorf("constraint %q does not take a value", name)
			}
		}
		out = append(out, rawConstraint{name: name, arg: arg})
	}
	return out, nil
}

// constraints is a tag with all parsing already done — numbers
// converted, membership lists split — ready to be checked against a
// value. This is what the naive path rebuilds per call.
type constraints struct {
	required bool
	nonzero  bool
	minSet   bool
	min      float64
	maxSet   bool
	max      float64
	email    bool
	allowed  []string
}

func compileConstraints(path, tag string) (constraints, error) {
	var c constraints
	raws, err := parseTag(tag)
	if err != nil {
		return c, fmt.Errorf("field %s: %w", path, err)
	}
	for _, rc := range raws {
		switch rc.name {
		case "required":
			c.required = true
		case "nonzero":
			c.nonzero = true
		case "email":
			c.email = true
		case "oneof":
			c.allowed = strings.Split(rc.arg, "|")
		case "min":
			n, perr := strconv.ParseFloat(rc.arg, 64)
			if perr != nil {
				return c, fmt.Errorf("field %s: min=%q is not a number", path, rc.arg)
			}
			c.minSet, c.min = true, n
		case "max":
			n, perr := strconv.ParseFloat(rc.arg, 64)
			if perr != nil {
				return c, fmt.Errorf("field %s: max=%q is not a number", path, rc.arg)
			}
			c.maxSet, c.max = true, n
		}
	}
	return c, nil
}

// checkApplicable fails closed when a constraint cannot make sense for
// the field's type — e.g. email on an int — instead of silently doing
// nothing at validation time.
func checkApplicable(path string, t reflect.Type, c constraints) error {
	hasMinMax := c.minSet || c.maxSet
	switch {
	case !hasMinMax && !c.email && len(c.allowed) == 0:
		return nil // required/nonzero apply to any type
	case hasMinMax:
		switch t.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64,
			reflect.String, reflect.Map, reflect.Slice, reflect.Array:
			return nil
		}
	case c.email || len(c.allowed) > 0:
		if t.Kind() == reflect.String {
			return nil
		}
	}
	return fmt.Errorf("field %s: constraint not applicable to %s", path, t)
}

// ---------------------------------------------------------------------------
// Compiled plans — what BuildSchema produces once and reuses forever
// ---------------------------------------------------------------------------

// fieldPlan is one field's fully resolved validation work: where to
// find it (index chain through any promoted embeds), what to check,
// and where to recurse for nested structs.
type fieldPlan struct {
	idx    []int       // index chain from the root struct value
	path   string      // dotted path used in error messages
	c      constraints // parsed tag
	isPtr  bool        // final hop is a pointer
	nested *structPlan // non-nil when the effective field type is a struct
}

// structPlan is the compiled plan for one struct type.
type structPlan struct {
	fields []fieldPlan
}

func buildStructPlan(t reflect.Type, prefix string) (*structPlan, error) {
	sp := &structPlan{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		ft := f.Type
		isPtr := false
		if ft.Kind() == reflect.Ptr {
			isPtr = true
			ft = ft.Elem()
			if ft.Kind() == reflect.Ptr {
				return nil, fmt.Errorf("field %s%s: pointer-to-pointer is unsupported", prefix, f.Name)
			}
		}
		path := prefix + f.Name

		if ft.Kind() == reflect.Struct && f.Anonymous {
			// Embedded structs are promoted even when the embedded
			// type's name is unexported: reading its exported fields
			// is legal reflection, and their paths belong at this level.
			if !isPtr {
				sub, err := buildStructPlan(ft, prefix)
				if err != nil {
					return nil, err
				}
				for j := range sub.fields {
					promoted := sub.fields[j]
					promoted.idx = append([]int{i}, promoted.idx...)
					sp.fields = append(sp.fields, promoted)
				}
				continue
			}
			// Embedded pointer-to-struct also promotes; its inner
			// paths are top-level and nil skips silently.
			sub, err := buildStructPlan(ft, prefix)
			if err != nil {
				return nil, err
			}
			sp.fields = append(sp.fields, fieldPlan{idx: []int{i}, isPtr: true, nested: sub})
			continue
		}

		if f.PkgPath != "" {
			continue // unexported: reflection cannot set/read meaningfully
		}

		fp := fieldPlan{idx: []int{i}, path: path, isPtr: isPtr}

		if ft.Kind() == reflect.Struct {
			// Named nested struct (or embedded pointer-to-struct):
			// compile its subtree; the wrapper's own tag may only use
			// required/nonzero.
			subPrefix := path + "."
			if f.Anonymous {
				// Embedded pointer promotes too; its inner paths are top-level.
				subPrefix = prefix
				fp.path = ""
			}
			sub, err := buildStructPlan(ft, subPrefix)
			if err != nil {
				return nil, err
			}
			fp.nested = sub
			c, cerr := compileConstraints(path, f.Tag.Get("validate"))
			if cerr != nil {
				return nil, cerr
			}
			if cerr := checkApplicable(path, ft, c); cerr != nil {
				return nil, cerr
			}
			fp.c = c
			sp.fields = append(sp.fields, fp)
			continue
		}

		c, cerr := compileConstraints(path, f.Tag.Get("validate"))
		if cerr != nil {
			return nil, cerr
		}
		if cerr := checkApplicable(path, ft, c); cerr != nil {
			return nil, cerr
		}
		fp.c = c
		sp.fields = append(sp.fields, fp)
	}
	return sp, nil
}

// walk follows the index chain from sv, dereferencing intermediate
// pointers. ok=false means an intermediate pointer was nil, leaving
// nothing at the destination to validate.
func walk(sv reflect.Value, idx []int) (fv reflect.Value, ok bool) {
	fv = sv
	for k, i := range idx {
		fv = fv.Field(i)
		if k < len(idx)-1 && fv.Kind() == reflect.Ptr {
			if fv.IsNil() {
				return reflect.Value{}, false
			}
			fv = fv.Elem()
		}
	}
	return fv, true
}

// run executes sp against sv (a struct value of the planned type),
// appending violations.
func (sp *structPlan) run(sv reflect.Value, errs *[]FieldError) {
	for i := range sp.fields {
		fp := &sp.fields[i]
		fv, ok := walk(sv, fp.idx)
		if !ok {
			continue // crossed a nil intermediate pointer: nothing to check
		}

		if fp.nested != nil {
			if fp.isPtr {
				if fv.IsNil() {
					if fp.c.required {
						*errs = append(*errs, FieldError{fp.path, "must not be nil (required)"})
					}
					continue // optional-nil skips remaining checks
				}
				fv = fv.Elem()
			}
			checkLeaf(fp.path, fv, fp.c, errs) // required/nonzero on the wrapper itself
			fp.nested.run(fv, errs)
			continue
		}

		if fp.isPtr {
			if fv.IsNil() {
				if fp.c.required {
					*errs = append(*errs, FieldError{fp.path, "must not be nil (required)"})
				}
				continue // optional-nil skips remaining checks
			}
			fv = fv.Elem()
		}
		checkLeaf(fp.path, fv, fp.c, errs)
	}
}

func numValue(fv reflect.Value) float64 {
	switch fv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(fv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(fv.Uint())
	default:
		return fv.Float()
	}
}

func lengthKind(k reflect.Kind) bool {
	switch k {
	case reflect.String, reflect.Map, reflect.Slice, reflect.Array:
		return true
	}
	return false
}

// checkLeaf applies parsed constraints to one present (non-nil) value.
func checkLeaf(path string, fv reflect.Value, c constraints, errs *[]FieldError) {
	fail := func(msg string) {
		*errs = append(*errs, FieldError{path, msg})
	}

	if c.required && fv.IsZero() {
		fail("must not be zero (required)")
		return // further checks on a zero value add noise, not signal
	}
	if c.nonzero && fv.IsZero() {
		fail("must be non-zero")
	}

	if c.minSet || c.maxSet {
		if lengthKind(fv.Kind()) {
			n := float64(fv.Len())
			if c.minSet && n < c.min {
				fail(fmt.Sprintf("length must be >= %s", strconv.FormatFloat(c.min, 'g', -1, 64)))
			}
			if c.maxSet && n > c.max {
				fail(fmt.Sprintf("length must be <= %s", strconv.FormatFloat(c.max, 'g', -1, 64)))
			}
		} else {
			n := numValue(fv)
			if c.minSet && n < c.min {
				fail(fmt.Sprintf("must be >= %s", strconv.FormatFloat(c.min, 'g', -1, 64)))
			}
			if c.maxSet && n > c.max {
				fail(fmt.Sprintf("must be <= %s", strconv.FormatFloat(c.max, 'g', -1, 64)))
			}
		}
	}

	if c.email {
		s := fv.String()
		at := strings.IndexByte(s, '@')
		if at <= 0 || at == len(s)-1 || strings.Contains(s[at+1:], "@") {
			fail("must be a valid email")
		}
	}

	if len(c.allowed) > 0 {
		if !slices.Contains(c.allowed, fv.String()) {
			fail(fmt.Sprintf("must be one of [%s]", strings.Join(c.allowed, ", ")))
		}
	}
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// derefStruct normalizes v to a struct value, reporting whether the
// input was usable at all.
func derefStruct(v any) (reflect.Value, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return rv, false
	}
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return rv, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return rv, false
	}
	return rv, true
}

// Validate walks v (a struct or pointer to struct) and enforces its
// tags, returning an aggregated ValidationError listing ALL violations.
// This is the naive path: tags are re-parsed on every call.
func Validate(v any) error {
	rv, ok := derefStruct(v)
	if !ok {
		return fmt.Errorf("tagforge: expected struct or non-nil pointer to struct, got %T", v)
	}
	sp, err := buildStructPlan(rv.Type(), "")
	if err != nil {
		return err
	}
	var errs []FieldError
	sp.run(rv, &errs)
	if len(errs) > 0 {
		return ValidationError(errs)
	}
	return nil
}

// Schema is the precompiled validation plan for one type: tags are
// parsed and checked once at BuildSchema time, so Validate runs
// without re-parsing.
type Schema struct {
	typ  reflect.Type
	plan *structPlan
}

// BuildSchema parses and validates all validate tags reachable from t
// (a struct or pointer-to-struct type), erroring on unknown types or
// constraints. Pay this cost once; run Schema.Validate many times.
func BuildSchema(t reflect.Type) (*Schema, error) {
	if t == nil {
		return nil, errors.New("tagforge: BuildSchema: nil type")
	}
	st := t
	for st.Kind() == reflect.Ptr {
		st = st.Elem()
	}
	if st.Kind() != reflect.Struct {
		return nil, fmt.Errorf("tagforge: BuildSchema: expected struct type, got %s", t)
	}
	plan, err := buildStructPlan(st, "")
	if err != nil {
		return nil, err
	}
	return &Schema{typ: st, plan: plan}, nil
}

// Validate executes the precompiled plan against v without re-parsing
// any tags. v must be a struct (or non-nil pointer to one) assignable
// to the schema's type.
func (sc *Schema) Validate(v any) error {
	if sc == nil || sc.plan == nil {
		return errors.New("tagforge: Validate called on unbuilt schema")
	}
	rv, ok := derefStruct(v)
	if !ok {
		return fmt.Errorf("tagforge: expected struct or non-nil pointer to struct, got %T", v)
	}
	if rv.Type() != sc.typ {
		return fmt.Errorf("tagforge: value of type %T does not match schema type %s", v, sc.typ)
	}
	var errs []FieldError
	sc.plan.run(rv, &errs)
	if len(errs) > 0 {
		return ValidationError(errs)
	}
	return nil
}
