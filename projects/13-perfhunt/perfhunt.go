// Package perfhunt is a deliberately slow HTTP stats service: a fixed
// embedded dataset served through an aggregation endpoint that commits
// every classic Go performance anti-pattern at once — string
// concatenation in loops, repeated map lookups, interface boxing in hot
// loops, regex compiled per call, repeated unbuffered scans, O(n²)
// deduplication, a sync.Mutex where a plain accumulation suffices, and
// reflection-heavy JSON marshaling through map[string]any.
//
// The exercise: profile it (pprof CPU/heap/block), read the flame
// graph, then rewrite the same functions so the benchmarks in
// solution/bench_test.go show measurable wins. The optimized versions
// live in the solution subpackage; slow.go there preserves the slow
// baseline verbatim for side-by-side benchmarking.
//
// The API surface is intentionally small and network-free where it
// matters: Aggregate and TopK are pure functions over plain slices, so
// benchmarks need no HTTP stack. Handler wraps them for realism.
package perfhunt

import (
	"net/http"
)

// Record is one observation in the dataset.
type Record struct {
	ID     string
	User   string
	Region string
	Tags   []string
	Value  float64
}

// Summary is the result of aggregating records.
type Summary struct {
	Count     int
	UniqueIDs int // distinct Record.ID values
	Total     float64
	Mean      float64
	Min       float64
	Max       float64
	ByRegion  map[string]float64 // summed Value per Region
}

// Count is one row of a TopK result: an item and how often it occurred.
type Count struct {
	Item  string
	Count int
}

// Aggregate folds records into a Summary in a single conceptual pass:
// total count of records, number of distinct IDs, sum/mean/min/max of
// Value, and per-region Value totals. An empty input yields the zero
// Summary (Mean/Min/Max are 0, ByRegion is non-nil but empty).
func Aggregate(records []Record) Summary {
	// TODO: Implement
	return Summary{}
}

// TopK counts items and returns the k most frequent as Count rows,
// ordered by descending count with ties broken by ascending item name.
// k <= 0 yields an empty slice; k larger than the number of distinct
// items yields all of them.
func TopK(items []string, k int) []Count {
	// TODO: Implement
	return nil
}

// Normalize canonicalizes a free-form tag: lowercase ASCII letters,
// keep digits, collapse every run of other characters into a single
// '-', and trim leading/trailing dashes. "  Hello,  World! " becomes
// "hello-world". It is called once per tag on hot paths — which is why
// compiling a regexp inside it matters.
func Normalize(s string) string {
	// TODO: Implement
	return ""
}

// dataset lazily builds the fixed embedded corpus once and returns the
// records plus a flattened tag stream (one entry per record tag, in
// generation order). Deterministic: same contents every run.
func dataset() ([]Record, []string) {
	// TODO: Implement
	return nil, nil
}

// Handler returns an http.HandlerFunc serving the embedded dataset:
//
//	GET /stats      -> JSON object mirroring Aggregate(dataset)
//	GET /tags?k=N   -> JSON array of TopK(allTags, N), N defaults to 5
//
// Any other path returns 404; any method other than GET returns 405.
func Handler() http.HandlerFunc {
	// TODO: Implement
	return nil
}
