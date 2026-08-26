package perfhunt

import (
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Correctness: slow and fast must agree (spec is shared, timing-free)
// ---------------------------------------------------------------------------

func TestSlowMatchesFastAggregate(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	recs := make([]Record, 400)
	for i := range recs {
		recs[i] = Record{
			ID:     string(rune('a' + rng.Intn(10))),
			Region: []string{"eu", "us", "ap"}[rng.Intn(3)],
			Tags:   []string{"x", "y"},
			Value:  float64(rng.Intn(1000)) / 4,
		}
	}
	if got, want := AggregateSlow(recs), Aggregate(recs); !reflect.DeepEqual(got, want) {
		t.Fatalf("slow %#v != fast %#v", got, want)
	}
	if got := AggregateSlow(nil); !reflect.DeepEqual(got, Aggregate(nil)) {
		t.Fatalf("empty input: slow %#v != fast %#v", got, Aggregate(nil))
	}
}

func TestSlowMatchesFastTopKAndNormalize(t *testing.T) {
	items := strings.Fields("b a c c b a d d d b e e f")
	for _, k := range []int{-1, 0, 1, 3, 100} {
		got, want := TopKSlow(items, k), TopK(items, k)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("TopK(k=%d): slow %v != fast %v", k, got, want)
		}
	}
	for _, s := range []string{
		"Hello", "  Hello,  World! ", "!!!", "", "A--B c", "digits 42!",
		// Non-ASCII contract cases (review round 1, F1): bytes >= 0x80
		// are separators; ToLower must not smuggle them into ASCII.
		"\u212A", "A\u212AB", "caf\u00E9", "\u00C5rge",
	} {
		if got, want := NormalizeSlow(s), Normalize(s); got != want {
			t.Fatalf("Normalize(%q): slow %q != fast %q", s, got, want)
		}
	}
}

func TestSlowMatchesFastParseK(t *testing.T) {
	// Overflow/edge agreement with strconv.Atoi (review round 1, F2):
	// huge k values must be REJECTED (ok=false) so both handlers keep
	// the default k=5, never silently wrapping.
	for _, raw := range []string{
		"99999999999999999999", "-99999999999999999999",
		"9223372036854775807", "-9223372036854775808",
		"9223372036854775808", "-9223372036854775809",
		"+12345678901234567890123", "18446744073709551616",
		"-1", "0", "42", "+7", "-x", "", "-", "+",
	} {
		gotN, gotOK := atoiSimple(raw)
		wantN, wantErr := strconv.Atoi(raw)
		if gotOK != (wantErr == nil) {
			t.Fatalf("atoiSimple(%q) ok=%v, Atoi err=%v — acceptance disagrees", raw, gotOK, wantErr)
		}
		if gotOK && gotN != wantN {
			t.Fatalf("atoiSimple(%q) = %d, Atoi = %d", raw, gotN, wantN)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmarks: slow baseline vs optimized rewrite
// ---------------------------------------------------------------------------

var benchRecords, benchTags = dataset()

func BenchmarkAggregate(b *testing.B) {
	b.Run("Slow", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = AggregateSlow(benchRecords)
		}
	})
	b.Run("Fast", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Aggregate(benchRecords)
		}
	})
}

func BenchmarkTopK(b *testing.B) {
	b.Run("Slow", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = TopKSlow(benchTags, 5)
		}
	})
	b.Run("Fast", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = TopK(benchTags, 5)
		}
	})
}

var normalizeInputs = func() []string {
	in := make([]string, 0, len(benchTags))
	for _, tg := range benchTags[:15000] { // bounded slice keeps sub-benchmarks comparable
		in = append(in, tg)
	}
	return in
}()

func BenchmarkNormalize(b *testing.B) {
	b.Run("Slow", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = NormalizeSlow(normalizeInputs[i%len(normalizeInputs)])
		}
	})
	b.Run("Fast", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Normalize(normalizeInputs[i%len(normalizeInputs)])
		}
	})
}

func benchHandler(h http.HandlerFunc) func(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	return func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			h(w, req)
			body, _ := io.ReadAll(w.Result().Body)
			_ = body
		}
	}
}

func BenchmarkHandlerStats(b *testing.B) {
	b.Run("Slow", benchHandler(HandlerSlow()))
	b.Run("Fast", benchHandler(Handler()))
}
