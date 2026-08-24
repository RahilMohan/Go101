package metricsd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// snapshotMap renders c.Snapshot and parses it into key->value pairs.
// Histogram bucket lines keep their full "name_bucket{le=\"b\"}" text
// as the key; _sum/_count lines use the bare suffix keys.
func snapshotMap(t *testing.T, c *Collector) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: unexpected error: %v", err)
	}
	m := make(map[string]string)
	for _, ln := range strings.Split(buf.String(), "\n") {
		if ln == "" {
			continue
		}
		key, val, ok := strings.Cut(ln, " ")
		if !ok {
			t.Fatalf("Snapshot: malformed line %q", ln)
		}
		m[key] = val
	}
	return m
}

// wantF formats a float exactly as Snapshot must.
func wantF(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// waitFor polls cond until true or the timeout elapses (checked every 2ms).
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, what)
}

// ---------------------------------------------------------------------------
// 1. Counter concurrency: the lock-free proof
// ---------------------------------------------------------------------------

func TestCounterConcurrentIncExactTotal(t *testing.T) {
	c := New()
	const goroutines, perG = 16, 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				c.Inc("requests")
			}
		}()
	}
	wg.Wait()

	got := snapshotMap(t, c)["requests"]
	if got != strconv.Itoa(goroutines*perG) {
		t.Fatalf("counter total = %s, want %d (lost updates = not atomic)", got, goroutines*perG)
	}
}

func TestCounterAddAccumulates(t *testing.T) {
	c := New()
	c.Add("bytes_in", 1024)
	c.Add("bytes_in", 2048)

	if got := snapshotMap(t, c)["bytes_in"]; got != "3072" {
		t.Fatalf("bytes_in = %s, want 3072", got)
	}
}

// ---------------------------------------------------------------------------
// 2. Gauge last-write-wins under concurrent sets
// ---------------------------------------------------------------------------

func TestGaugeLastWriteWinsIsOneOfWrittenValues(t *testing.T) {
	c := New()
	const n = 32

	written := make([]float64, n)
	for i := range written {
		written[i] = float64(100 + i)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(v float64) {
			defer wg.Done()
			<-start // maximize contention on the swap
			c.SetGauge("queue_depth", v)
		}(written[i])
	}
	close(start)
	wg.Wait()

	got := snapshotMap(t, c)["queue_depth"]
	for _, want := range written {
		if got == wantF(want) {
			return
		}
	}
	t.Fatalf("gauge = %q, want one of the %d written values (torn or missing write)", got, n)
}

// ---------------------------------------------------------------------------
// 3. Observe bucketing table + bounds-locked-at-first-use
// ---------------------------------------------------------------------------

func TestObserveBucketingTable(t *testing.T) {
	bounds := []float64{0.5, 1, 4}

	tests := []struct {
		value    float64
		wantLe05 int64
		wantLe1  int64
		wantLe4  int64
		wantInf  int64
	}{
		{0.25, 1, 1, 1, 1},
		{0.75, 1, 2, 2, 2},
		{2.5, 1, 2, 3, 3},
		{99, 1, 2, 3, 4}, // above every bound lands in +Inf only
	}

	c := New()
	for _, tc := range tests {
		c.Observe("latency", tc.value, bounds)
	}

	m := snapshotMap(t, c)
	checks := []struct {
		key  string
		want string
	}{
		{`latency_bucket{le="0.5"}`, strconv.FormatInt(tests[len(tests)-1].wantLe05, 10)},
		{`latency_bucket{le="1"}`, strconv.FormatInt(tests[len(tests)-1].wantLe1, 10)},
		{`latency_bucket{le="4"}`, strconv.FormatInt(tests[len(tests)-1].wantLe4, 10)},
		{`latency_bucket{le="+Inf"}`, strconv.FormatInt(tests[len(tests)-1].wantInf, 10)},
		{"latency_count", "4"},
	}
	for _, chk := range checks {
		if got := m[chk.key]; got != chk.want {
			t.Errorf("%s = %s, want %s", chk.key, got, chk.want)
		}
	}
}

func TestObserveBoundsLockedAtFirstUse(t *testing.T) {
	c := New()
	c.Observe("latency", 0.25, []float64{0.5, 1})
	before := snapshotMap(t, c)

	// A later call with different bounds must be ignored entirely:
	// neither the value nor the alternative buckets may appear.
	c.Observe("latency", 9, []float64{2, 8})

	after := snapshotMap(t, c)
	if len(after) != len(before) {
		t.Fatalf("snapshot grew from %d to %d lines after mismatched-bounds Observe", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("line %q changed from %q to %q after mismatched-bounds Observe", k, v, after[k])
		}
	}
}

func TestObserveSumAndCount(t *testing.T) {
	c := New()
	// Values chosen to be exact in binary floating point.
	c.Observe("size", 0.25, []float64{0.5})
	c.Observe("size", 0.75, []float64{0.5})
	c.Observe("size", 2.5, []float64{0.5})

	m := snapshotMap(t, c)
	if got := m["size_sum"]; got != "3.5" {
		t.Errorf("size_sum = %s, want 3.5", got)
	}
	if got := m["size_count"]; got != "3" {
		t.Errorf("size_count = %s, want 3", got)
	}
}

// ---------------------------------------------------------------------------
// 4. Snapshot format golden test
// ---------------------------------------------------------------------------

func TestSnapshotGoldenFormat(t *testing.T) {
	c := New()
	c.Inc("http_requests")
	c.Add("bytes_in", 2048)
	c.SetGauge("queue_depth", 3.5)
	c.SetGauge("active", 2)
	c.Observe("latency", 0.25, []float64{0.5, 1, 4})
	c.Observe("latency", 0.75, []float64{0.5, 1, 4})
	c.Observe("latency", 2.5, []float64{0.5, 1, 4})

	want := "" +
		"bytes_in 2048\n" +
		"http_requests 1\n" +
		"active 2\n" +
		"queue_depth 3.5\n" +
		`latency_bucket{le="0.5"} 1` + "\n" +
		`latency_bucket{le="1"} 2` + "\n" +
		`latency_bucket{le="4"} 3` + "\n" +
		`latency_bucket{le="+Inf"} 3` + "\n" +
		"latency_sum 3.5\n" +
		"latency_count 3\n"

	var buf bytes.Buffer
	if err := c.Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := buf.String(); got != want {
		t.Fatalf("Snapshot mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSnapshotEmptyCollectorRendersNothing(t *testing.T) {
	var buf bytes.Buffer
	if err := New().Snapshot(&buf); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("empty collector rendered %q, want empty output", buf.String())
	}
}

// ---------------------------------------------------------------------------
// 5. Scraper against httptest: parsing, ring buffer, error accounting
// ---------------------------------------------------------------------------

func TestScraperTwoTicksParseAndErrorTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stats":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"workers":{"active":7}}`)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := New()
	s := NewScraper(c, srv.Client())

	targets := []ScrapeTarget{
		{Name: "pool", URL: srv.URL + "/stats", JSONPath: "workers.active"},
		{Name: "flaky", URL: srv.URL + "/broken", JSONPath: "x"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, targets, 20*time.Millisecond)
	defer s.Stop()

	waitFor(t, 5*time.Second, "two scraped points in pool history",
		func() bool { return len(s.History("pool")) >= 2 })

	hist := s.History("pool")
	if len(hist) != 2 && len(hist) > ringSize {
		t.Fatalf("history length %d exceeds ring capacity", len(hist))
	}
	for i, p := range hist {
		if p.Value != 7 {
			t.Errorf("pool history[%d].Value = %v, want 7 (dotted-path parse)", i, p.Value)
		}
		if p.At.IsZero() {
			t.Errorf("pool history[%d].At is zero", i)
		}
	}

	// Oldest -> newest ordering.
	if !hist[0].At.Before(hist[1].At) && !hist[0].At.Equal(hist[1].At) {
		t.Errorf("history not oldest->newest: %v then %v", hist[0].At, hist[1].At)
	}

	m := snapshotMap(t, c)
	errCount, err := strconv.ParseInt(m["flaky_errors"], 10, 64)
	if err != nil || errCount < 1 {
		t.Fatalf("flaky_errors = %q (%v), want >= 1 after failed fetches", m["flaky_errors"], err)
	}
	if last, ok := m["flaky_last"]; ok {
		t.Errorf("flaky_last = %q present, want absent (failed fetches must not set gauge)", last)
	}
	if got := s.History("flaky"); len(got) != 0 {
		t.Errorf("flaky history has %d points, want 0 (errors skip buffer writes)", len(got))
	}

	poolLast, ok := m["pool_last"]
	if !ok || poolLast != "7" {
		t.Errorf("pool_last = %q (present=%v), want \"7\"", poolLast, ok)
	}
}

// ---------------------------------------------------------------------------
// 6. Tick discipline
// ---------------------------------------------------------------------------

func TestTickDisciplineIntervalHonored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"v":1}`)
	}))
	defer srv.Close()

	c := New()
	s := NewScraper(c, srv.Client())
	targets := []ScrapeTarget{{Name: "tick", URL: srv.URL, JSONPath: "v"}}

	s.Start(context.Background(), targets, 10*time.Millisecond)
	time.Sleep(250 * time.Millisecond)
	s.Stop()

	n := len(s.History("tick"))
	if n < 15 || n > 30 {
		t.Fatalf("recorded %d ticks in ~250ms at a 10ms interval, want between 15 and 30", n)
	}
}

// ---------------------------------------------------------------------------
// 7. Stop idempotency
// ---------------------------------------------------------------------------

func TestStopIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"v":1}`)
	}))
	defer srv.Close()

	c := New()
	s := NewScraper(c, srv.Client())
	targets := []ScrapeTarget{{Name: "tick", URL: srv.URL, JSONPath: "v"}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Start(context.Background(), targets, 50*time.Millisecond)
		time.Sleep(20 * time.Millisecond)
		s.Stop()
		s.Stop() // second call must be a safe no-op
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop deadlocked or was not idempotent")
	}
}

// ---------------------------------------------------------------------------
// 8. Handler integration (/metrics and /history/{name})
// ---------------------------------------------------------------------------

func TestHandlerMetricsAndHistoryIntegration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"workers":{"active":7}}`)
	}))
	defer srv.Close()

	c := New()
	c.Inc("hits")
	s := NewScraper(c, srv.Client())
	targets := []ScrapeTarget{{Name: "pool", URL: srv.URL + "/", JSONPath: "workers.active"}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, targets, time.Hour) // one immediate tick, then quiet
	defer s.Stop()
	waitFor(t, 5*time.Second, "one scraped point",
		func() bool { return len(s.History("pool")) >= 1 })

	h := Handler(c, s)

	// /metrics
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hits 1\n") {
		t.Errorf("GET /metrics body missing counter line:\n%s", body)
	}
	if !strings.Contains(body, "pool_last 7\n") {
		t.Errorf("GET /metrics body missing scraped gauge line:\n%s", body)
	}

	// /history/{name}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/history/pool", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /history/pool status = %d, want 200", rec.Code)
	}
	var pts []Point
	if err := json.Unmarshal(rec.Body.Bytes(), &pts); err != nil {
		t.Fatalf("GET /history/pool body is not JSON Points: %v\n%s", err, rec.Body.String())
	}
	if len(pts) != 1 || pts[0].Value != 7 || pts[0].At.IsZero() {
		t.Errorf("GET /history/pool = %+v, want one point with Value 7 and non-zero At", pts)
	}

	// Unknown target renders an empty JSON array.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/history/nope", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /history/nope status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" && got != "null" {
		t.Errorf("GET /history/nope = %q, want empty array", got)
	}
}
