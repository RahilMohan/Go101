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
//   - Struct elements of slices/maps/arrays ARE validated recursively
//     ("Items[0].Code", "ByID[k].Port"); nil pointer elements skip
//     silently, like optional fields. Scalar collections stay
//     length-only.
//   - Recursive struct types (self- or mutually-referential through any
//     chain of pointer/nested/collection hops) are rejected at BUILD
//     time with "recursive type not supported": their validation plans
//     would never terminate. Cycle detection tracks the current path's
//     types only — diamond-shaped reuse of one type at several depths
//     remains legal.
//   - A nil pointer inside a struct is a violation iff required;
//     an optional nil pointer skips its remaining checks entirely.
//   - Unexported fields are ignored silently: reflection cannot set
//     them meaningfully, so promising to validate them would lie.
package tagforge

import (
	"errors"
	"fmt"
	"math"
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
			n, perr := parseStrictNumber(path, "min", rc.arg)
			if perr != nil {
				return c, perr
			}
			c.minSet, c.min = true, n
		case "max":
			n, perr := parseStrictNumber(path, "max", rc.arg)
			if perr != nil {
				return c, perr
			}
			c.maxSet, c.max = true, n
		}
	}
	return c, nil
}

// parseStrictNumber accepts only plain decimal numbers for min/max:
// strconv.ParseFloat alone would also admit NaN, Inf, hex floats
// ("0x1p3"), and underscores — none of which belong in a validation
// tag. Anything but a plain decimal is rejected at build time.
func parseStrictNumber(path, name, arg string) (float64, error) {
	if strings.ContainsAny(strings.ToLower(arg), "xni_") {
		return 0, fmt.Errorf("field %s: %s=%q is not a plain decimal number", path, name, arg)
	}
	n, err := strconv.ParseFloat(arg, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("field %s: %s=%q is not a plain decimal number", path, name, arg)
	}
	return n, nil
}

// checkApplicable fails closed when a constraint cannot make sense for
// the field's type — e.g. email on an int — instead of silently doing
// nothing at validation time.
// checkApplicable validates EACH constraint family independently
// against the field's kind at build time — so a co-resident inapplicable
// constraint (e.g. "min=1,email" on an int) is rejected up front instead
// of failing spuriously at validation time.
func checkApplicable(path string, t reflect.Type, c constraints) error {
	notApplicable := func(name string) error {
		return fmt.Errorf("field %s: constraint %q not applicable to %s", path, name, t)
	}
	if c.minSet && !numericKind(t.Kind()) && !lengthKind(t.Kind()) {
		return notApplicable("min")
	}
	if c.maxSet && !numericKind(t.Kind()) && !lengthKind(t.Kind()) {
		return notApplicable("max")
	}
	if c.email && t.Kind() != reflect.String {
		return notApplicable("email")
	}
	if len(c.allowed) > 0 && t.Kind() != reflect.String {
		return notApplicable("oneof")
	}
	return nil // required/nonzero apply to any type
}

// ---------------------------------------------------------------------------
// Compiled plans — what BuildSchema produces once and reuses forever
// ---------------------------------------------------------------------------

// fieldPlan is one field's fully resolved validation work: where to
// find it (index chain through any promoted embeds), what to check,
// and where to recurse for nested structs or struct elements of
// slices/maps/arrays.
type fieldPlan struct {
	idx         []int       // index chain from the root struct value
	path        string      // dotted path used in error messages
	childPrefix string      // path prefix handed to nested plans ("path." / "")
	c           constraints // parsed tag
	isPtr       bool        // final hop is a pointer
	nested      *structPlan // non-nil when the effective field type is a struct
	elems       *structPlan // non-nil for slice/map/array fields whose elements are structs
}

// structPlan is the compiled plan for one struct type.
type structPlan struct {
	fields []fieldPlan
}

// maxDepth bounds validation recursion. Plans are acyclic by
// construction (recursive types are rejected at build time), so this
// is defense-in-depth against pathological data shapes, not a
// correctness requirement.
const maxDepth = 64

func buildStructPlan(t reflect.Type, prefix string, stack []reflect.Type) (*structPlan, error) {
	// Cycle detection tracks the CURRENT PATH's types, not global
	// visited-set: one type may legitimately appear at several depths
	// via different paths (diamonds), but a type appearing twice on one
	// path means infinite recursion ahead — reject it at build time.
	for _, st := range stack {
		if st == t {
			return nil, fmt.Errorf("%srecursive type %s not supported", prefix, t)
		}
	}
	stack = append(stack, t)

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
			sub, err := buildStructPlan(ft, prefix, stack)
			if err != nil {
				return nil, err
			}
			if !isPtr {
				for j := range sub.fields {
					promoted := sub.fields[j]
					promoted.idx = append([]int{i}, promoted.idx...)
					sp.fields = append(sp.fields, promoted)
				}
				continue
			}
			// Embedded pointer-to-struct also promotes; its inner
			// paths are top-level and nil skips silently.
			sp.fields = append(sp.fields, fieldPlan{idx: []int{i}, path: "", isPtr: true, nested: sub})
			continue
		}

		if f.PkgPath != "" {
			continue // unexported: reflection cannot set/read meaningfully
		}

		fp := fieldPlan{idx: []int{i}, path: path, isPtr: isPtr}

		if ft.Kind() == reflect.Struct {
			// Named nested struct: compile its subtree; the wrapper's
			// own tag may only use required/nonzero.
			sub, err := buildStructPlan(ft, path+".", stack)
			if err != nil {
				return nil, err
			}
			fp.nested = sub
			fp.childPrefix = path + "."
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

		// Collections of structs get their ELEMENTS validated
		// recursively (paths like "Items[0].Code"); scalar collections
		// stay length-only. Nil pointer elements skip silently.
		switch ft.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			et := ft.Elem()
			if et.Kind() == reflect.Ptr {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				ep, eerr := buildStructPlan(et, "", stack)
				if eerr != nil {
					return nil, fmt.Errorf("field %s: element type: %w", path, eerr)
				}
				fp.elems = ep
			}
		}

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
	sp.runAt(sv, "", 0, errs)
}

// runAt is run with a dynamic path prefix (used for collection
// elements, whose indices/keys only exist at runtime) and a depth
// guard.
func (sp *structPlan) runAt(sv reflect.Value, prefix string, depth int, errs *[]FieldError) {
	if depth > maxDepth {
		*errs = append(*errs, FieldError{prefix, "nesting too deep"})
		return
	}
	for i := range sp.fields {
		fp := &sp.fields[i]
		fv, ok := walk(sv, fp.idx)
		if !ok {
			continue // crossed a nil intermediate pointer: nothing to check
		}
		// Fast path: at the root (and inside element plans, which are
		// compiled prefix-free) the build-time paths are already
		// absolute, so no per-call concatenation is needed. Only
		// collection elements, whose indices/keys exist only at
		// runtime, pay for string building.
		fullPath := fp.path
		childPrefix := fp.childPrefix
		if prefix != "" {
			fullPath = prefix + fp.path
			if fp.nested != nil && fp.path != "" {
				childPrefix = fullPath + "."
			} else {
				childPrefix = prefix
			}
		}

		if fp.nested != nil {
			if fp.isPtr {
				if fv.IsNil() {
					if fp.c.required {
						*errs = append(*errs, FieldError{fullPath, "must not be nil (required)"})
					}
					continue // optional-nil skips remaining checks
				}
				fv = fv.Elem()
			}
			checkLeaf(fullPath, fv, fp.c, errs) // required/nonzero on the wrapper itself
			fp.nested.runAt(fv, childPrefix, depth+1, errs)
			continue
		}

		if fp.isPtr {
			if fv.IsNil() {
				if fp.c.required {
					*errs = append(*errs, FieldError{fullPath, "must not be nil (required)"})
				}
				continue // optional-nil skips remaining checks
			}
			fv = fv.Elem()
		}
		checkLeaf(fullPath, fv, fp.c, errs)

		if fp.elems != nil {
			runElems(fp.elems, fullPath, fv, depth+1, errs)
		}
	}
}

// runElems validates struct elements of a slice/array/map field,
// building indexed element paths like "Items[0]" or "ByID[k]".
func runElems(ep *structPlan, fieldPath string, fv reflect.Value, depth int, errs *[]FieldError) {
	switch fv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < fv.Len(); i++ {
			runElem(ep, fv.Index(i), fmt.Sprintf("%s[%d]", fieldPath, i), depth, errs)
		}
	case reflect.Map:
		for iter := fv.MapRange(); iter.Next(); {
			runElem(ep, iter.Value(),
				fmt.Sprintf("%s[%v]", fieldPath, iter.Key().Interface()), depth, errs)
		}
	}
}

func runElem(ep *structPlan, ev reflect.Value, elemPath string, depth int, errs *[]FieldError) {
	if ev.Kind() == reflect.Ptr {
		if ev.IsNil() {
			return // nil pointer element: skipped silently, like optional fields
		}
		ev = ev.Elem()
	}
	ep.runAt(ev, elemPath+".", depth, errs)
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

func numericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
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
	sp, err := buildStructPlan(rv.Type(), "", nil)
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
	plan, err := buildStructPlan(st, "", nil)
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
