package tagforge

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 1. Table-driven constraint coverage: every constraint x pass/fail x types
// ---------------------------------------------------------------------------

func TestConstraints(t *testing.T) {
	type reqInt struct {
		N int `validate:"required"`
	}
	_ = reqInt{}
	type minMaxInt struct {
		N int `validate:"min=1,max=10"`
	}
	type minMaxFloat struct {
		F float64 `validate:"min=0.5,max=2.5"`
	}
	type minLenString struct {
		S string `validate:"min=3"`
	}
	type maxLenSlice struct {
		X []int `validate:"max=2"`
	}
	type minLenMap struct {
		M map[string]int `validate:"min=1"`
	}
	type emailField struct {
		E string `validate:"email"`
	}
	type oneofField struct {
		Lvl string `validate:"oneof=low|mid|high"`
	}
	type nonzeroBool struct {
		B bool `validate:"nonzero"`
	}
	type unsignedMin struct {
		U uint `validate:"min=5"`
	}

	tests := []struct {
		name    string
		val     any
		wantErr bool
	}{
		// required
		{"required/zero-int fails", reqInt{}, true},
		{"required/nonzero-int passes", struct {
			N int `validate:"required"`
		}{N: -1}, false},
		{"required/empty-string fails", struct {
			S string `validate:"required"`
		}{}, true},
		{"required/false-bool fails", nonzeroBool{}, true},
		{"required/true-bool passes", nonzeroBool{B: true}, false},
		{"required/zero-float fails", struct {
			F float64 `validate:"required"`
		}{}, true},

		// min/max numeric
		{"min/int-below fails", minMaxInt{N: 0}, true},
		{"min/int-edge passes", minMaxInt{N: 1}, false},
		{"min+max/int-above fails", minMaxInt{N: 11}, true},
		{"min+max/int-top-edge passes", minMaxInt{N: 10}, false},
		{"min/float-below fails", minMaxFloat{F: 0.4}, true},
		{"min+max/float-passes", minMaxFloat{F: 2.5}, false},
		{"min/uint-passes", unsignedMin{U: 5}, false},
		{"min/uint-fails", unsignedMin{U: 4}, true},
		{"min/negative-int-violates-min=1", minMaxInt{N: -3}, true},

		// min/max on lengths
		{"min/string-too-short fails", minLenString{S: "ab"}, true},
		{"min/string-edge passes", minLenString{S: "abc"}, false},
		{"max/slice-too-long fails", maxLenSlice{X: []int{1, 2, 3}}, true},
		{"max/slice-edge passes", maxLenSlice{X: []int{1, 2}}, false},
		{"min/map-empty fails", minLenMap{}, true},
		{"min/map-one-entry passes", minLenMap{M: map[string]int{"a": 1}}, false},

		// email
		{"email/no-at fails", emailField{E: "ops.example.com"}, true},
		{"email/two-ats fails", emailField{E: "a@@b.com"}, true},
		{"email/empty-local fails", emailField{E: "@example.com"}, true},
		{"email/empty-domain fails", emailField{E: "ops@"}, true},
		{"email/simple passes", emailField{E: "ops@example.com"}, false},

		// oneof
		{"oneof/member passes", oneofField{Lvl: "mid"}, false},
		{"oneof/non-member fails", oneofField{Lvl: "extreme"}, true},
		{"oneof/empty fails", oneofField{}, true},

		// no tags at all
		{"untagged/always passes", struct{ Anything int }{Anything: 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.val)
			if got := err != nil; got != tt.wantErr {
				t.Fatalf("Validate(%#v) error = %v, wantErr = %v", tt.val, err, tt.wantErr)
			}

			// Every case must behave identically through a prebuilt schema.
			sc, scErr := BuildSchema(reflect.TypeOf(tt.val))
			if scErr != nil {
				t.Fatalf("BuildSchema: unexpected error: %v", scErr)
			}
			err = sc.Validate(tt.val)
			if got := err != nil; got != tt.wantErr {
				t.Fatalf("Schema.Validate(%#v) error = %v, wantErr = %v", tt.val, err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Aggregation: all violations reported, each with its field path
// ---------------------------------------------------------------------------

type aggSub struct {
	Timeout int `validate:"min=10"`
}

type aggCfg struct {
	Name   string   `validate:"required,min=3"`
	Email  string   `validate:"email"`
	Server aggSub   `validate:"required"`
	Tags   []string `validate:"min=1"`
}

func TestAggregatesAllViolations(t *testing.T) {
	v := aggCfg{
		Name:  "",             // required + min -> counts as required violation only
		Email: "not-an-email", // email violation
		Tags:  nil,            // min length violation
		// Server is a zero struct: required violation, and its Timeout
		// field also violates min=10.
	}
	err := Validate(v)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"Name", "Email", "Server", "Server.Timeout", "Tags"} {
		if !strings.Contains(msg, want) {
			t.Errorf("aggregated error missing %q:\n%s", want, msg)
		}
	}
	var ve ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error should be a ValidationError, got %T", err)
	}
	if len(ve) < 5 {
		t.Errorf("expected >=5 aggregated FieldErrors, got %d: %v", len(ve), ve)
	}
}

// ---------------------------------------------------------------------------
// 3. Nested + embedded structs
// ---------------------------------------------------------------------------

type embBase struct {
	ID string `validate:"required,min=2"`
}

type nestedServer struct {
	Timeout int    `validate:"min=1"`
	Region  string `validate:"oneof=us|eu|ap"`
}

type nestedCfg struct {
	embBase // promoted: ID validates at top-level path "ID"
	Server  nestedServer
	DB      struct {
		Host string `validate:"required"`
		Port int    `validate:"min=1,max=65535"`
	}
}

func TestNestedAndEmbeddedPaths(t *testing.T) {
	good := nestedCfg{
		embBase: embBase{ID: "svc-1"},
		Server:  nestedServer{Timeout: 5, Region: "eu"},
	}
	good.DB.Host = "db.internal"
	good.DB.Port = 5432
	if err := Validate(good); err != nil {
		t.Fatalf("valid nested config rejected: %v", err)
	}

	bad := nestedCfg{
		Server: nestedServer{Timeout: 0, Region: "moon"},
	}
	bad.ID = ""     // promoted violation at top level
	bad.DB.Port = 0 // nested anonymous-struct violation

	err := Validate(bad)
	if err == nil {
		t.Fatal("expected violations, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"ID",      // embedded promotion: top-level path
		"DB.Port", // dotted path into anonymous struct field
		"Server.Timeout",
		"Server.Region",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "embBase.ID") {
		t.Errorf("embedded field should be promoted, not qualified:\n%s", msg)
	}
}

// ---------------------------------------------------------------------------
// 4. Pointer handling
// ---------------------------------------------------------------------------

type ptrCfg struct {
	Name *string `validate:"required,min=2"`
	Nick *string `validate:"min=2"`
}

func strp(s string) *string { return &s }

func TestPointerHandling(t *testing.T) {
	// Required nil pointer: violation.
	err := Validate(ptrCfg{})
	if err == nil || !strings.Contains(err.Error(), "Name") {
		t.Fatalf("nil required pointer should violate Name, got %v", err)
	}

	// Optional nil pointer: remaining checks skipped, no violation.
	err = Validate(ptrCfg{Name: strp("ab")})
	if err != nil {
		t.Fatalf("optional nil pointer should be skipped, got %v", err)
	}

	// Non-nil but violating value behind the pointer.
	err = Validate(ptrCfg{Name: strp("a"), Nick: strp("x")})
	if err == nil || !strings.Contains(err.Error(), "Nick") {
		t.Fatalf("derefed violations should be checked, got %v", err)
	}

	// All good.
	if err := Validate(ptrCfg{Name: strp("ab"), Nick: strp("nk")}); err != nil {
		t.Fatalf("valid pointers rejected: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5. Unknown constraint -> fail-closed, naming field and token
// ---------------------------------------------------------------------------

func TestUnknownConstraint(t *testing.T) {
	type badTag struct {
		X int `validate:"required,frobnicate"`
	}
	v := badTag{}

	err := Validate(v)
	if err == nil {
		t.Fatal("Validate must fail closed on unknown constraint")
	}
	if !strings.Contains(err.Error(), "X") || !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error should name field X and token frobnicate, got: %v", err)
	}

	_, err = BuildSchema(reflect.TypeOf(v))
	if err == nil {
		t.Fatal("BuildSchema must fail closed on unknown constraint")
	}
	if !strings.Contains(err.Error(), "X") || !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error should name field X and token frobnicate, got: %v", err)
	}

	// Malformed argument values are also build-time errors.
	type badNum struct {
		Y int `validate:"min=banana"`
	}
	if _, err := BuildSchema(reflect.TypeOf(badNum{})); err == nil {
		t.Fatal("BuildSchema must reject non-numeric min argument")
	}

	// Constraints that cannot apply to the type are rejected too.
	type badKind struct {
		Z int `validate:"email"`
	}
	if _, err := BuildSchema(reflect.TypeOf(badKind{})); err == nil {
		t.Fatal("BuildSchema must reject email constraint on non-string field")
	}
	if err := Validate(badKind{}); err == nil {
		t.Fatal("Validate must fail closed on inapplicable constraint")
	}

	// EACH family must be checked independently, even when co-resident
	// with an applicable one — min=1 is fine on int, email is not.
	type badCombo struct {
		Z int `validate:"min=1,email"`
	}
	_, err = BuildSchema(reflect.TypeOf(badCombo{}))
	if err == nil {
		t.Fatal("BuildSchema must reject co-resident min+email on int")
	}
	if !strings.Contains(err.Error(), "email") {
		t.Errorf("error should name the offending email constraint, got: %v", err)
	}
	if err := Validate(badCombo{}); err == nil {
		t.Fatal("Validate must also fail closed on co-resident min+email")
	}

	type badCombo2 struct {
		Y int `validate:"max=5,oneof=a|b"`
	}
	_, err = BuildSchema(reflect.TypeOf(badCombo2{}))
	if err == nil {
		t.Fatal("BuildSchema must reject co-resident max+oneof on int")
	}
	if !strings.Contains(err.Error(), "oneof") {
		t.Errorf("error should name the offending oneof constraint, got: %v", err)
	}
}

func TestStrictNumberParsing(t *testing.T) {
	rejected := []string{"nan", "NaN", "inf", "-Inf", "Infinity", "0x1p3", "1_000", "1e309"}
	for _, arg := range rejected {
		typ := reflect.StructOf([]reflect.StructField{
			{Name: "N", Type: reflect.TypeOf(0), Tag: reflect.StructTag(`validate:"min=` + arg + `"`)},
		})
		if _, err := BuildSchema(typ); err == nil {
			t.Errorf("BuildSchema must reject min=%q", arg)
		}
	}

	accepted := []string{"0", "-3", "+5", ".5", "1.5e-3"}
	for _, arg := range accepted {
		typ := reflect.StructOf([]reflect.StructField{
			{Name: "N", Type: reflect.TypeOf(0), Tag: reflect.StructTag(`validate:"min=` + arg + `"`)},
		})
		if _, err := BuildSchema(typ); err != nil {
			t.Errorf("BuildSchema should accept min=%q, got %v", arg, err)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. Non-struct input
// ---------------------------------------------------------------------------

func TestNonStructInput(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"int", 42},
		{"string", "hello"},
		{"slice", []int{1}},
		{"map", map[string]string{}},
		{"nil", nil},
		{"nil-pointer", (*ptrCfg)(nil)},
	}
	for _, tc := range cases {
		t.Run("Validate/"+tc.name, func(t *testing.T) {
			if err := Validate(tc.val); err == nil {
				t.Fatalf("Validate(%v) should reject non-struct input", tc.val)
			}
		})
	}

	for _, typ := range []reflect.Type{
		reflect.TypeOf(42),
		reflect.TypeOf(""),
		nil,
	} {
		if _, err := BuildSchema(typ); err == nil {
			t.Fatalf("BuildSchema(%v) should reject non-struct type", typ)
		}
	}

	// Pointer-to-struct is accepted.
	if err := Validate(&ptrCfg{Name: strp("ab")}); err != nil {
		t.Errorf("pointer-to-struct should validate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 7. Schema reuse correctness
// ---------------------------------------------------------------------------

func TestSchemaReuse(t *testing.T) {
	sc, err := BuildSchema(reflect.TypeOf(ptrCfg{}))
	if err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}

	// Same schema, two different values: one fails, then one passes,
	// then another failure — state must not leak between runs.
	if err := sc.Validate(ptrCfg{}); err == nil {
		t.Error("first value should fail (nil required)")
	}
	if err := sc.Validate(ptrCfg{Name: strp("ok")}); err != nil {
		t.Errorf("second value should pass, got %v", err)
	}
	if err := sc.Validate(ptrCfg{Name: strp("n"), Nick: strp("z")}); err == nil {
		t.Error("third value should fail (short strings)")
	}
	if err := sc.Validate(42); err == nil {
		t.Error("schema must reject mismatched type")
	}
}

// ---------------------------------------------------------------------------
// 8. Unexported fields ignored silently — documented behavior
// ---------------------------------------------------------------------------

type unexportedCfg struct {
	secret string `validate:"required,this-is-not-a-real-constraint"`
	Pub    string `validate:"required"`
}

func TestUnexportedFieldsIgnored(t *testing.T) {
	// The impossible tag on the unexported field must NOT trigger the
	// fail-closed unknown-constraint path, and an unset secret must not
	// count as a missing required value.
	v := unexportedCfg{Pub: "visible"}
	if err := Validate(v); err != nil {
		t.Fatalf("unexported fields must be ignored silently, got: %v", err)
	}

	sc, err := BuildSchema(reflect.TypeOf(v))
	if err != nil {
		t.Fatalf("BuildSchema with unexported tagged field: %v", err)
	}
	if err := sc.Validate(v); err != nil {
		t.Fatalf("schema validation must ignore unexported fields, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 9. FuzzTagGrammar: arbitrary tag strings never panic
// ---------------------------------------------------------------------------

func FuzzTagGrammar(f *testing.F) {
	seeds := []string{
		"",
		" ",
		"required",
		"min=3",
		"min",
		"min=",
		"=",
		"a=b=c",
		"oneof=a|b|c",
		"email required nonzero",
		"\x00\xff",
		"min=99999999999999999999999999",
		"required\x00frobnicate=-1",
		"ONEOF=x",
		"min=+5 max=-2.5e3",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		constraints, err := parseTag(tag)
		if err != nil && constraints != nil {
			t.Errorf("parseTag(%q): error must imply nil constraints", tag)
		}
		if err == nil && tag == "" && len(constraints) != 0 {
			t.Errorf("parseTag(\"\"): empty tag must parse to zero constraints")
		}
	})
}

// ---------------------------------------------------------------------------
// Benchmarks — the lesson's proof: naive reflection vs precompiled schema
// ---------------------------------------------------------------------------

type benchNested struct {
	Region string `validate:"required,oneof=us|eu|ap"`
}

type benchCfg struct {
	Service string  `validate:"required,min=2,max=64"`
	Owner   string  `validate:"required,email"`
	Workers int     `validate:"min=1,max=256"`
	Ratio   float64 `validate:"min=0,max=1"`
	Enabled bool    `validate:"nonzero"`
	Nested  benchNested
}

var benchValue = benchCfg{
	Service: "api-gateway",
	Owner:   "ops@example.com",
	Workers: 8,
	Ratio:   0.75,
	Enabled: true,
	Nested:  benchNested{Region: "eu"},
}

// BenchmarkValidateReflect pays full price every call: reflection to
// walk fields AND tag parsing for every field of every call.
func BenchmarkValidateReflect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if err := Validate(benchValue); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkValidateSchema parses tags once (outside the loop) and then
// executes the precompiled plan — the whole point of BuildSchema.
func BenchmarkValidateSchema(b *testing.B) {
	sc, err := BuildSchema(reflect.TypeOf(benchValue))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sc.Validate(benchValue); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBuildSchema isolates the one-time cost that
// BenchmarkValidateReflect re-pays on every single call.
func BenchmarkBuildSchema(b *testing.B) {
	typ := reflect.TypeOf(benchValue)
	for i := 0; i < b.N; i++ {
		if _, err := BuildSchema(typ); err != nil {
			b.Fatal(err)
		}
	}
}
