// slow.go is the preserved "intentionally slow baseline" commit for
// Project 13. This repo commits once per project on the feature branch,
// so this file IS the deliberate anti-pattern snapshot referenced by
// the master plan: every classic Go performance mistake lives here,
// side-by-side with the optimized rewrites in perfhunt.go, and
// bench_test.go measures them against each other.
//
// Each function is behaviorally identical to its fast counterpart (the
// shared test suite plus TestSlowMatchesFast pin that down) — it is
// only slower. Suffixes: AggregateSlow, TopKSlow, NormalizeSlow,
// HandlerSlow.
package perfhunt

import (
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// AggregateSlow computes exactly what Aggregate computes, but:
//
//   - guards every float accumulation with a sync.Mutex where a plain
//     local variable (or atomic) would do;
//   - boxes each Value into an []any before summing it back out;
//   - does repeated full scans of the records slice (sum pass, min
//     pass, max pass) instead of one buffered pass;
//   - performs three map lookups per record on ByRegion;
//   - dedups IDs with an O(n²) linear scan over a growing slice;
//   - builds a string fingerprint per record via += in a loop.
func AggregateSlow(records []Record) Summary {
	if len(records) == 0 {
		return Summary{ByRegion: map[string]float64{}}
	}
	var mu sync.Mutex // anti-pattern: lock where none is needed

	var boxed []any // anti-pattern: interface boxing in the hot loop
	for _, r := range records {
		mu.Lock()
		boxed = append(boxed, r.Value)
		mu.Unlock()
	}
	var boxedTotal float64
	for _, v := range boxed { // unboxing pass to recover the sum
		if f, ok := v.(float64); ok {
			boxedTotal += f
		}
	}

	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, r := range records { // unbuffered scan #2: min
		if r.Value < minV {
			minV = r.Value
		}
	}
	for _, r := range records { // unbuffered scan #3: max
		if r.Value > maxV {
			maxV = r.Value
		}
	}

	byRegion := map[string]float64{}
	var uniqIDs []string
	for _, r := range records {
		// anti-pattern: string concatenation in a loop
		fingerprint := ""
		for _, tg := range r.Tags {
			fingerprint += tg + "|"
		}
		_ = fingerprint

		mu.Lock()
		_ = boxedTotal // touch under lock: pure overhead theater
		mu.Unlock()

		// anti-pattern: repeated map lookups
		if _, ok := byRegion[r.Region]; !ok {
			byRegion[r.Region] = 0
		}
		byRegion[r.Region] = byRegion[r.Region] + r.Value

		// anti-pattern: O(n²) dedup
		dup := false
		for _, id := range uniqIDs {
			if id == r.ID {
				dup = true
				break
			}
		}
		if !dup {
			uniqIDs = append(uniqIDs, r.ID)
		}
	}

	count := len(records)
	var grandTotal float64
	for _, r := range records { // scan #1 was here all along: the sum
		grandTotal += r.Value
	}
	return Summary{
		Count:     count,
		UniqueIDs: len(uniqIDs),
		Total:     grandTotal,
		Mean:      grandTotal / float64(count),
		Min:       minV,
		Max:       maxV,
		ByRegion:  byRegion,
	}
}

// TopKSlow counts items but dedups through an O(n²) contains-scan and
// takes a mutex for every single increment of the counter map.
func TopKSlow(items []string, k int) []Count {
	if k <= 0 {
		return []Count{}
	}
	var mu sync.Mutex
	var distinct []string
	counts := make(map[string]int)
	for _, it := range items {
		mu.Lock()
		// O(n²) dedup over a growing slice
		known := false
		for _, d := range distinct {
			if d == it {
				known = true
				break
			}
		}
		if !known {
			distinct = append(distinct, it)
		}
		counts[it]++
		mu.Unlock()
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

// NormalizeSlow compiles a fresh regexp on EVERY call — the canonical
// hidden-cost mistake — then rebuilds the result with += piecewise.
func NormalizeSlow(s string) string {
	re := regexp.MustCompile(`[^a-z0-9]+`) // compiled per call!
	parts := re.Split(strings.ToLower(s), -1)
	out := ""
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i > 0 && out != "" {
			out += "-"
		}
		out += part
	}
	return out
}

// HandlerSlow mirrors Handler's routing but serializes responses by
// building a map[string]any and handing it to encoding/json — maximal
// reflection, boxing every number into an interface value per request.
func HandlerSlow() http.HandlerFunc {
	muxLike := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/stats":
			recs, _ := dataset()
			s := Aggregate(recs)
			payload := map[string]any{
				"count":      s.Count,
				"unique_ids": s.UniqueIDs,
				"total":      s.Total,
				"mean":       s.Mean,
				"min":        s.Min,
				"max":        s.Max,
				"by_region":  s.ByRegion,
			}
			body, err := json.Marshal(payload)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck // best-effort write to client

		case "/tags":
			_, allTags := dataset()
			k := 5
			if raw := r.URL.Query().Get("k"); raw != "" {
				if parsed, ok := atoiSimple(raw); ok {
					k = parsed
				}
			}
			rows := TopK(allTags, k)
			list := make([]map[string]any, len(rows))
			for i, row := range rows {
				list[i] = map[string]any{"item": row.Item, "count": row.Count}
			}
			body, err := json.Marshal(list)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body) //nolint:errcheck // best-effort write to client

		default:
			http.NotFound(w, r)
		}
	}
	return muxLike
}

func atoiSimple(s string) (int, bool) {
	neg, i := false, 0
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		i = 1
	}
	if i >= len(s) {
		return 0, false
	}
	n := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, true
}
