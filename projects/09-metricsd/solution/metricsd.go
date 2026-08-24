// Package metricsd is a small metrics daemon: an in-process collector
// with lock-free counters and gauges, a background scraper that polls
// HTTP endpoints for numeric values into per-target time-series ring
// buffers, and a Prometheus-text-format /metrics endpoint. It teaches
// sync/atomic operations, lock-free writes, fixed-capacity ring
// buffers, tick-disciplined background loops, and the Prometheus text
// exposition format.
//
// Concurrency design notes:
//
//   - Counters are atomic.Int64 values reached through a map. The map
//     needs a lock only on first use of a name; after that the hot
//     path is a shared RLock'd map probe plus one atomic add — no
//     write lock, no mutex held during the arithmetic itself.
//   - Gauges are atomic.Pointer[float64]: last-value-wins via one
//     pointer swap, no CAS loop needed because there is no
//     read-modify-write dependency between writes.
//   - Histogram sums accumulate with a CAS loop on the float64 bits:
//     float addition is not associative, so plain atomic stores would
//     lose updates and CompareAndSwap makes each add read-modify-write
//     atomic without a mutex.
//   - Ring buffers use a per-target mutex rather than an atomic write
//     index. Justification: exactly one goroutine ever writes a given
//     ring (the scrape loop), and History reads are rare, so the mutex
//     is effectively uncontended; it removes all memory-ordering
//     subtleties of hand-rolled atomic indices at zero measurable cost
//     for scrape frequencies (seconds to milliseconds per tick).
package metricsd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ringSize is the fixed number of retained samples per scrape target.
// 64 is "enough for ~1h of history at a 1m scrape interval".
const ringSize = 64

// Collector holds counters, gauges, and histograms with lock-free
// record paths once a metric name has been registered.
type Collector struct {
	mu       sync.RWMutex
	counters map[string]*atomic.Int64
	gauges   map[string]*atomic.Pointer[float64]
	histos   map[string]*histogram
}

type histogram struct {
	bounds  []float64      // sorted ascending, as provided by first use
	counts  []atomic.Int64 // len(bounds)+1; last index is the +Inf bucket
	sumBits atomic.Uint64  // float64 bits of the running sum, CAS-updated
}

// New returns an empty collector.
func New() *Collector {
	return &Collector{
		counters: make(map[string]*atomic.Int64),
		gauges:   make(map[string]*atomic.Pointer[float64]),
		histos:   make(map[string]*histogram),
	}
}

// Inc increments counter name by one. Lock-free via atomic.Int64.
func (c *Collector) Inc(name string) {
	c.Add(name, 1)
}

// Add adds v to counter name (monotonically increasing). Lock-free via
// atomic.Int64.
func (c *Collector) Add(name string, v int64) {
	c.mu.RLock()
	ctr, ok := c.counters[name]
	c.mu.RUnlock()
	if ok {
		ctr.Add(v)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if ctr, ok = c.counters[name]; !ok { // lost the creation race
		ctr = new(atomic.Int64)
		c.counters[name] = ctr
	}
	ctr.Add(v)
}

// SetGauge sets gauge name to v; last write wins via atomic pointer
// swap.
func (c *Collector) SetGauge(name string, v float64) {
	p := new(float64)
	*p = v

	c.mu.RLock()
	g, ok := c.gauges[name]
	c.mu.RUnlock()
	if ok {
		g.Store(p)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if g, ok = c.gauges[name]; !ok {
		g = new(atomic.Pointer[float64])
		c.gauges[name] = g
	}
	g.Store(p)
}

// Observe records value v in histogram name. Buckets are defined at
// first use from bucketBounds (sorted internally); subsequent calls
// with different bounds for the same name are ignored entirely.
// Buckets are cumulative Prometheus-style le buckets with a trailing
// +Inf bucket.
func (c *Collector) Observe(name string, v float64, bucketBounds []float64) {
	sorted := append([]float64(nil), bucketBounds...)
	sort.Float64s(sorted)

	c.mu.RLock()
	h, ok := c.histos[name]
	c.mu.RUnlock()

	if !ok {
		c.mu.Lock()
		h, ok = c.histos[name]
		if !ok {
			h = &histogram{
				bounds: sorted,
				counts: make([]atomic.Int64, len(sorted)+1),
			}
			c.histos[name] = h
		}
		c.mu.Unlock()
	}

	if !equalFloats(h.bounds, sorted) {
		return // bounds locked at first use; mismatched observation ignored
	}
	h.record(v)
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// record places v in exactly one bucket and adds it to the sum.
func (h *histogram) record(v float64) {
	idx := len(h.bounds) // default: above every bound -> +Inf bucket
	for i, b := range h.bounds {
		if v <= b {
			idx = i
			break
		}
	}
	h.counts[idx].Add(1)

	for {
		old := h.sumBits.Load()
		newSum := math.Float64frombits(old) + v
		if h.sumBits.CompareAndSwap(old, math.Float64bits(newSum)) {
			return
		}
	}
}

// fmtF renders a float the way Prometheus text format does here: the
// shortest representation that round-trips.
func fmtF(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Snapshot renders the current state to w in the Prometheus text
// exposition format: one "name N" line per counter and gauge, and for
// each histogram cumulative "name_bucket{le=\"b\"} N" lines plus
// _sum/_count lines. Names are emitted in sorted order within each
// section (counters, then gauges, then histograms).
func (c *Collector) Snapshot(w io.Writer) error {
	c.mu.RLock()
	counterNames := make([]string, 0, len(c.counters))
	for n := range c.counters {
		counterNames = append(counterNames, n)
	}
	gaugeNames := make([]string, 0, len(c.gauges))
	for n := range c.gauges {
		gaugeNames = append(gaugeNames, n)
	}
	histoNames := make([]string, 0, len(c.histos))
	for n := range c.histos {
		histoNames = append(histoNames, n)
	}
	counters := make(map[string]int64, len(c.counters))
	for n, ctr := range c.counters {
		counters[n] = ctr.Load()
	}
	gauges := make(map[string]float64, len(c.gauges))
	for n, g := range c.gauges {
		gauges[n] = *g.Load()
	}
	histos := make(map[string]*histogram, len(c.histos))
	for n, h := range c.histos {
		// Counts and sums stay readable after RUnlock because each is
		// individually atomic. Accepted tradeoff: a snapshot is
		// eventually-consistent, not linearizable — lines may reflect
		// different instants (a counter read after a histogram bucket,
		// etc.). That is fine for exposition endpoints: Prometheus
		// scrapes are point-in-time samples of an approximation, and
		// full coherence would force every write to take the same lock.
		histos[n] = h
	}
	c.mu.RUnlock()

	sort.Strings(counterNames)
	sort.Strings(gaugeNames)
	sort.Strings(histoNames)

	var err error
	write := func(format string, args ...any) {
		if _, werr := fmt.Fprintf(w, format, args...); werr != nil && err == nil {
			err = werr
		}
	}

	for _, n := range counterNames {
		write("%s %d\n", n, counters[n])
	}
	for _, n := range gaugeNames {
		write("%s %s\n", n, fmtF(gauges[n]))
	}
	for _, n := range histoNames {
		h := histos[n]
		var cumulative int64
		for i, b := range h.bounds {
			cumulative += h.counts[i].Load()
			write("%s_bucket{le=%q} %d\n", n, fmtF(b), cumulative)
		}
		cumulative += h.counts[len(h.bounds)].Load()
		write("%s_bucket{le=\"+Inf\"} %d\n", n, cumulative)
		write("%s_sum %s\n", n, fmtF(math.Float64frombits(h.sumBits.Load())))
		write("%s_count %d\n", n, cumulative)
	}
	return err
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

// ring is a fixed-capacity FIFO overwriting the oldest sample when
// full. See the package comment for the mutex-over-atomic-index
// rationale.
type ring struct {
	mu   sync.Mutex
	buf  [ringSize]Point
	next int // next slot to overwrite
	n    int // number of valid samples (<= ringSize)
}

func (r *ring) add(p Point) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = p
	r.next = (r.next + 1) % ringSize
	if r.n < ringSize {
		r.n++
	}
}

// snapshot returns a fresh oldest->newest copy.
func (r *ring) snapshot() []Point {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Point, r.n)
	start := (r.next - r.n + ringSize) % ringSize
	copy(out, r.buf[start:])
	if start+r.n > ringSize { // wrapped: copy the tail from the front
		copy(out[ringSize-start:], r.buf[:r.n-(ringSize-start)])
	}
	return out
}

// Scraper polls targets on an interval into a ring buffer per target.
type Scraper struct {
	collector *Collector
	client    *http.Client

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once

	mu    sync.Mutex
	rings map[string]*ring
}

// NewScraper returns a scraper reporting scraped values into c. If
// client is nil, http.DefaultClient is used.
func NewScraper(c *Collector, client *http.Client) *Scraper {
	if client == nil {
		client = http.DefaultClient
	}
	return &Scraper{
		collector: c,
		client:    client,
		rings:     make(map[string]*ring),
	}
}

// Start launches a goroutine ticking every interval and returns
// immediately. The first scrape happens immediately on start. Each
// tick fetches all targets concurrently. A target's value lands in its
// ring buffer AND updates collector gauge "<name>_last". Fetch errors
// increment collector counter "<name>_errors" and skip the buffer
// write. Calling Start more than once is undefined. Stop must happen
// after Start (the cancel field is unsynchronized by design — set only
// in Start, read only in Stop).
func (s *Scraper) Start(ctx context.Context, targets []ScrapeTarget, interval time.Duration) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	tg := append([]ScrapeTarget(nil), targets...) // defensive copy
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		s.scrapeAll(ctx, tg) // immediate first tick
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.scrapeAll(ctx, tg)
			}
		}
	}()
}

// scrapeAll fetches every target concurrently (one goroutine per
// target; the bound is len(targets), no worker pool needed).
func (s *Scraper) scrapeAll(ctx context.Context, targets []ScrapeTarget) {
	var wg sync.WaitGroup
	wg.Add(len(targets))
	for i := range targets {
		go func(tg ScrapeTarget) {
			defer wg.Done()
			s.scrapeOne(ctx, tg)
		}(targets[i])
	}
	wg.Wait()
}

func (s *Scraper) scrapeOne(ctx context.Context, tg ScrapeTarget) {
	fail := func() { s.collector.Add(tg.Name+"_errors", 1) }

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tg.URL, nil)
	if err != nil {
		fail()
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		fail()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail()
		return
	}

	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		fail()
		return
	}
	v, err := jsonPathValue(root, tg.JSONPath)
	if err != nil {
		fail()
		return
	}

	p := Point{At: time.Now(), Value: v}
	s.mu.Lock()
	r, ok := s.rings[tg.Name]
	if !ok {
		r = new(ring)
		s.rings[tg.Name] = r
	}
	s.mu.Unlock()

	r.add(p)
	s.collector.SetGauge(tg.Name+"_last", v)
}

// jsonPathValue walks a dotted path ("workers.active") through decoded
// JSON maps and converts the leaf to float64.
func jsonPathValue(root any, path string) (float64, error) {
	cur := root
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("json path %q: %q is not inside an object", path, part)
		}
		cur, ok = m[part]
		if !ok {
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

// History returns the ring contents oldest->newest for target name as
// a fresh copy (never more than ringSize points). Unknown names return
// an empty slice.
func (s *Scraper) History(name string) []Point {
	s.mu.Lock()
	r := s.rings[name]
	s.mu.Unlock()
	if r == nil {
		return []Point{}
	}
	return r.snapshot()
}

// Stop cancels the internal loop and waits for exit. Stop is
// idempotent and safe for concurrent use. Behavior after Stop is
// undefined only for Start (see Start); History remains safe.
func (s *Scraper) Stop() {
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	s.wg.Wait()
}

// Handler returns an http.Handler serving GET /metrics (Snapshot) and
// GET /history/{name} (JSON array of Points). It is what cmd/metricsd
// mounts.
func Handler(c *Collector, s *Scraper) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if err := c.Snapshot(w); err != nil {
			http.Error(w, "snapshot failed", http.StatusInternalServerError)
		}
	})
	mux.HandleFunc("GET /history/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.History(r.PathValue("name")))
	})
	return mux
}
