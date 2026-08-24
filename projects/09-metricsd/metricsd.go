// Package metricsd is a small metrics daemon: an in-process collector
// with lock-free counters and gauges, a background scraper that polls
// HTTP endpoints for numeric values into per-target time-series ring
// buffers, and a Prometheus-text-format /metrics endpoint. It teaches
// sync/atomic operations, lock-free writes, fixed-capacity ring
// buffers, tick-disciplined background loops, and the Prometheus text
// exposition format.
package metricsd

import (
	"context"
	"io"
	"net/http"
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
func (c *Collector) Snapshot(w io.Writer) error {
	// TODO: Implement
	return nil
}

// ScrapeTarget is one HTTP endpoint to poll for a numeric value.
type ScrapeTarget struct {
	Name     string
	URL      string
	JSONPath string // simple dotted path into a JSON object, e.g. "workers.active"
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
// client is nil, http.DefaultClient is used.
func NewScraper(c *Collector, client *http.Client) *Scraper {
	// TODO: Implement
	return &Scraper{}
}

// Start launches a goroutine ticking every interval and returns
// immediately. The first scrape happens immediately on start. Each
// tick fetches all targets concurrently. A target's value lands in its
// ring buffer AND updates collector gauge "<name>_last". Fetch errors
// increment collector counter "<name>_errors" and skip the buffer
// write. Calling Start more than once is undefined.
func (s *Scraper) Start(ctx context.Context, targets []ScrapeTarget, interval time.Duration) {
	// TODO: Implement
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
