// Package metricsd is a small metrics daemon: an in-process collector
// with lock-free counters and gauges, a background scraper that polls
// HTTP endpoints for numeric values into per-target time-series ring
// buffers, and a Prometheus-text-format /metrics endpoint. It teaches
// sync/atomic operations, lock-free writes, fixed-capacity ring
// buffers, tick-disciplined background loops, and the Prometheus text
// exposition format.
//
// Teaching scope / abuse caveats (see fleet abuse review):
//   - Cardinality is unbounded: each distinct Inc/Add/SetGauge/Observe
//     name and each ScrapeTarget.Name grows maps/rings without eviction.
//     Callers must bound distinct names and target count (e.g. <10k).
//     Production registries cap cardinality and validate names.
//   - Rings are bounded per-target (ringSize=64) but unbounded in count.
//     Goroutines per tick = len(targets) — cap targets (e.g. 256) and
//     bound bodies via LimitReader if hardening.
//   - Scrape timeout is bounded: NewScraper defaults to 10s when client
//     is nil; daemon binary passes -scrape-timeout (default 10s) via
//     http.Client{Timeout}. Always supply a client with Timeout; a nil/
//     zero timeout would let one hung target freeze scrapeAll via wg.Wait.
package metricsd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ringSize is the fixed number of retained samples per scrape target.
// 64 is "enough for ~1h of history at a 1m scrape interval".
const ringSize = 64

// Collector holds counters and gauges with lock-free writes.
type Collector struct {
	_ struct{}
}

// New returns an empty collector.
func New() *Collector {
	// TODO: Implement
	return &Collector{}
}

// Inc increments counter name by one. Lock-free via atomic.Int64.
func (c *Collector) Inc(name string) {
	// TODO: Implement
}

// Add adds v to counter name (monotonically increasing). Lock-free via
// atomic.Int64.
func (c *Collector) Add(name string, v int64) {
	// TODO: Implement
}

// SetGauge sets gauge name to v; last write wins via atomic pointer
// swap.
func (c *Collector) SetGauge(name string, v float64) {
	// TODO: Implement
}

// Observe records value v in histogram name. Buckets are defined at
// first use from bucketBounds (sorted internally); subsequent calls
// with different bounds for the same name are ignored entirely.
// Buckets are cumulative Prometheus-style le buckets with a trailing
// +Inf bucket.
func (c *Collector) Observe(name string, v float64, bucketBounds []float64) {
	// TODO: Implement
}

// Snapshot renders the current state to w in the Prometheus text
// exposition format: one "name N" line per counter and gauge, and for
// each histogram cumulative "name_bucket{le=\"b\"} N" lines plus
// _sum/_count lines. Names are emitted in sorted order within each
// section (counters, then gauges, then histograms).
//
// C3: gauge loads must nil-check (if p := g.Load(); p != nil) because
// publication order (map insert + Store under write lock) is fragile
// across refactors; Snapshot in the reference solution is hardened.
func (c *Collector) Snapshot(w io.Writer) error {
	// TODO: Implement
	return nil
}

// ScrapeTarget is one HTTP endpoint to poll for a numeric value.
type ScrapeTarget struct {
	Name     string
	URL      string
	JSONPath string // simple dotted path into a JSON object, e.g. "workers.active"; empty means root must be numeric
}

// Point is one scraped sample.
type Point struct {
	At    time.Time
	Value float64
}

// Scraper polls targets on an interval into a ring buffer per target.
type Scraper struct {
	_ struct{}
}

// NewScraper returns a scraper reporting scraped values into c. If
// client is nil, a 10s-timeout client is used (DefaultClient has no
// timeout — one stalled body would freeze all scraping). Callers that
// supply a client should set Timeout (10s default in cmd/metricsd).
func NewScraper(c *Collector, client *http.Client) *Scraper {
	// TODO: Implement — reference solution defaults nil to
	// &http.Client{Timeout: 10*time.Second} for C1/abuse hardening.
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	_ = client
	return &Scraper{}
}

// Start launches a goroutine ticking every interval and returns
// immediately. The first scrape happens immediately on start. Each
// tick fetches all targets concurrently. A target's value lands in its
// ring buffer AND updates collector gauge "<name>_last". Fetch errors
// increment collector counter "<name>_errors" and skip the buffer
// write. Calling Start more than once is undefined. Stop must happen
// after Start (the cancel field is unsynchronized by design — set only
// in Start, read only in Stop).
//
// C1: interval <= 0 is guarded to 1s to avoid time.NewTicker panic.
// Reference solution does: if interval <= 0 { interval = time.Second }.
func (s *Scraper) Start(ctx context.Context, targets []ScrapeTarget, interval time.Duration) {
	// TODO: Implement — guard: if interval <= 0 { interval = time.Second }
	if interval <= 0 {
		interval = time.Second
	}
	_ = ctx
	_ = targets
}

// History returns the ring contents oldest->newest for target name as
// a fresh copy (never more than ringSize points). Unknown names return
// an empty slice.
func (s *Scraper) History(name string) []Point {
	// TODO: Implement
	return nil
}

// Stop cancels the internal loop and waits for exit. Stop is
// idempotent and safe for concurrent use. Behavior after Stop is
// undefined only for Start (see Start); History remains safe.
func (s *Scraper) Stop() {
	// TODO: Implement
}

// Handler returns an http.Handler serving GET /metrics (Snapshot) and
// GET /history/{name} (JSON array of Points). It is what cmd/metricsd
// mounts.
func Handler(c *Collector, s *Scraper) http.Handler {
	// TODO: Implement
	return nil
}

// jsonPathValue is the reference helper for dotted-path extraction.
// C2: empty path gracefully returns root numeric value or error — no
// panic, no probe for key "" via strings.Split("", ".").
func jsonPathValue(root any, path string) (float64, error) {
	if path == "" {
		switch n := root.(type) {
		case json.Number:
			return n.Float64()
		case float64:
			return n, nil
		default:
			return 0, fmt.Errorf("json path %q: empty path requires numeric root, got %T", path, root)
		}
	}
	cur := root
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("json path %q: %q is not inside an object", path, part)
		}
		var ok2 bool
		cur, ok2 = m[part]
		if !ok2 {
			return 0, fmt.Errorf("json path %q: key %q not found", path, part)
		}
	}
	switch n := cur.(type) {
	case json.Number:
		return n.Float64()
	case float64:
		return n, nil
	default:
		return 0, fmt.Errorf("json path %q: leaf is %T, want number", path, cur)
	}
}

var _ = io.Discard
