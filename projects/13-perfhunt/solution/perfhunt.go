// Package perfhunt holds the optimized version of the perfhunt stats
// service. Every function here is a rewrite of the anti-pattern-ridden
// baseline preserved verbatim in slow.go; see OPTIMIZATIONS.md for the
// anti-pattern -> fix -> expected profile evidence table and
// bench_test.go for side-by-side benchmarks.
//
// The tests in this directory are copied verbatim from the root
// package — both implementations must satisfy the same spec.
package perfhunt

import (
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// Aggregate folds records into a Summary in one pass over the input:
// total count of records, number of distinct IDs, sum/mean/min/max of
// Value, and per-region Value totals. An empty input yields the zero
// Summary (Mean/Min/Max are 0, ByRegion is non-nil but empty).
func Aggregate(records []Record) Summary {
	sum := Summary{ByRegion: make(map[string]float64, 8)}
	if len(records) == 0 {
		return sum
	}
	seen := make(map[string]struct{}, len(records))
	minV, maxV := math.Inf(1), math.Inf(-1)
	for i := range records {
		r := &records[i]
		sum.Count++
		sum.Total += r.Value
		if r.Value < minV {
			minV = r.Value
		}
		if r.Value > maxV {
			maxV = r.Value
		}
		sum.ByRegion[r.Region] += r.Value
		seen[r.ID] = struct{}{}
	}
	sum.UniqueIDs = len(seen)
	sum.Mean = sum.Total / float64(sum.Count)
	sum.Min = minV
	sum.Max = maxV
	return sum
}

// TopK counts items in a single map pass and returns the k most
// frequent rows ordered by descending count, ties broken by ascending
// item name. k <= 0 yields an empty slice.
func TopK(items []string, k int) []Count {
	if k <= 0 {
		return []Count{}
	}
	counts := make(map[string]int, 64)
	for _, it := range items {
		counts[it]++
	}
	rows := make([]Count, 0, len(counts))
	for item, n := range counts {
		rows = append(rows, Count{Item: item, Count: n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Item < rows[j].Item
	})
	if len(rows) > k {
		rows = rows[:k]
	}
	return rows
}

// Normalize canonicalizes a free-form tag without any regexp: lowercase
// ASCII letters, keep digits, collapse every run of other characters
// into a single '-', trim leading/trailing dashes. One allocation (the
// result), no per-call compilation.
func Normalize(s string) string {
	b := make([]byte, 0, len(s))
	pendingDash := true // start "after a dash" so leading runs are trimmed
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
			b = append(b, c)
			pendingDash = false
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b = append(b, c)
			pendingDash = false
		default:
			if !pendingDash {
				b = append(b, '-')
				pendingDash = true
			}
		}
	}
	return strings.TrimSuffix(string(b), "-")
}

// Handler serves the embedded dataset. JSON responses are assembled by
// hand with strconv.AppendFloat into a reusable buffer — no reflection,
// no intermediate map[string]any boxing.
//
//	GET /stats      -> Aggregate(dataset) as JSON
//	GET /tags?k=N   -> TopK(allTags, N) as JSON array (default N=5)
func Handler() http.HandlerFunc {
	const statsPrefix = `{"count":`
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/stats":
			recs, _ := dataset()
			s := Aggregate(recs)
			buf := make([]byte, 0, 512)
			buf = append(buf, statsPrefix...)
			buf = strconv.AppendInt(buf, int64(s.Count), 10)
			buf = append(buf, `,"unique_ids":`...)
			buf = strconv.AppendInt(buf, int64(s.UniqueIDs), 10)
			buf = append(buf, `,"total":`...)
			buf = strconv.AppendFloat(buf, s.Total, 'g', -1, 64)
			buf = append(buf, `,"mean":`...)
			buf = strconv.AppendFloat(buf, s.Mean, 'g', -1, 64)
			buf = append(buf, `,"min":`...)
			buf = strconv.AppendFloat(buf, s.Min, 'g', -1, 64)
			buf = append(buf, `,"max":`...)
			buf = strconv.AppendFloat(buf, s.Max, 'g', -1, 64)
			buf = append(buf, `,"by_region":{`...)
			regions := make([]string, 0, len(s.ByRegion))
			for region := range s.ByRegion {
				regions = append(regions, region)
			}
			sort.Strings(regions)
			for i, region := range regions {
				if i > 0 {
					buf = append(buf, ',')
				}
				buf = strconv.AppendQuote(buf, region)
				buf = append(buf, ':')
				buf = strconv.AppendFloat(buf, s.ByRegion[region], 'g', -1, 64)
			}
			buf = append(buf, '}', '}')

			w.Header().Set("Content-Type", "application/json")
			w.Write(buf) //nolint:errcheck // best-effort write to client

		case "/tags":
			_, allTags := dataset()
			k := 5
			if raw := r.URL.Query().Get("k"); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil {
					k = parsed
				}
			}
			rows := TopK(allTags, k)
			buf := make([]byte, 0, 256)
			buf = append(buf, '[')
			for i, row := range rows {
				if i > 0 {
					buf = append(buf, ',')
				}
				buf = append(buf, `{"item":`...)
				buf = strconv.AppendQuote(buf, row.Item)
				buf = append(buf, `,"count":`...)
				buf = strconv.AppendInt(buf, int64(row.Count), 10)
				buf = append(buf, '}')
			}
			buf = append(buf, ']')

			w.Header().Set("Content-Type", "application/json")
			w.Write(buf) //nolint:errcheck // best-effort write to client

		default:
			http.NotFound(w, r)
		}
	}
}

var (
	datasetOnce sync.Once
	datasetRecs []Record
	datasetTags []string
)

// dataset lazily builds the fixed embedded corpus once and returns the
// records plus a flattened tag stream. Deterministic: seeded PRNG, so
// every process run sees identical contents (deliberate duplicate IDs
// exercise the dedup path).
func dataset() ([]Record, []string) {
	datasetOnce.Do(func() {
		rng := rand.New(rand.NewSource(42))
		regions := []string{"us-east", "us-west", "eu-central", "ap-south"}
		tagPool := []string{
			"alpha", "beta", "gamma", "delta",
			"epsilon", "zeta", "eta", "theta",
			"Beta ", "--GAMMA--", // variants that exercise Normalize paths
		}
		users := make([]string, 200)
		for i := range users {
			users[i] = "user-" + strconv.Itoa(i)
		}
		const n = 5000
		recs := make([]Record, n)
		allTags := make([]string, 0, n*3)
		for i := range recs {
			tags := make([]string, 3)
			for j := range tags {
				tags[j] = tagPool[rng.Intn(len(tagPool))]
				allTags = append(allTags, tags[j])
			}
			recs[i] = Record{
				ID:     "rec-" + strconv.Itoa(rng.Intn(n/2)), // ~50% duplicate rate
				User:   users[rng.Intn(len(users))],
				Region: regions[rng.Intn(len(regions))],
				Tags:   tags,
				Value:  float64(rng.Intn(100_00)) / 100,
			}
		}
		datasetRecs = recs
		datasetTags = allTags
	})
	return datasetRecs, datasetTags
}
