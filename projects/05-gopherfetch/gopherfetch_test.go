package gopherfetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// concurrencyTracker counts in-flight requests and records the maximum
// observed concurrency, proving (or disproving) the pool bound.
type concurrencyTracker struct {
	inFlight int64
	maxSeen  int64
}

func (t *concurrencyTracker) handler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&t.inFlight, 1)
		for {
			old := atomic.LoadInt64(&t.maxSeen)
			if cur <= old || atomic.CompareAndSwapInt64(&t.maxSeen, old, cur) {
				break
			}
		}
		next(w, r)
		atomic.AddInt64(&t.inFlight, -1)
	}
}

func TestCheckRespectsWorkerPoolBound(t *testing.T) {
	var tracker concurrencyTracker
	srv := httptest.NewServer(tracker.handler(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Millisecond) // widen each request so overlaps are observable
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	urls := make([]string, 50)
	for i := range urls {
		urls[i] = srv.URL + "/link-" + strconv.Itoa(i)
	}

	c := NewChecker(5, 0)
	results := c.Check(context.Background(), urls)

	if len(results) != len(urls) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(urls))
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d] (%s) unexpected error: %v", i, r.URL, r.Err)
		}
		if r.Status != http.StatusOK {
			t.Errorf("results[%d] status = %d, want %d", i, r.Status, http.StatusOK)
		}
	}
	if max := atomic.LoadInt64(&tracker.maxSeen); max > 5 {
		t.Errorf("max concurrent in-flight requests = %d, want <= 5", max)
	} else if max < 2 {
		t.Errorf("max concurrent in-flight requests = %d, want >= 2 (pool never parallelized)", max)
	}
}

func TestCheckPreservesInputOrder(t *testing.T) {
	const n = 12
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx, err := strconv.Atoi(r.URL.Path[len("/u/"):])
		if err != nil {
			t.Errorf("bad path %q: %v", r.URL.Path, err)
		}
		// Later URLs finish earlier: a completion-order bug would shuffle
		// results even if scheduling were otherwise correct.
		time.Sleep(time.Duration(n-idx) * 5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	urls := make([]string, n)
	for i := range urls {
		urls[i] = fmt.Sprintf("%s/u/%d", srv.URL, i)
	}

	results := NewChecker(4, 0).Check(context.Background(), urls)

	for i, r := range results {
		if r.URL != urls[i] {
			t.Fatalf("results[%d].URL = %q, want %q (results not in input order)", i, r.URL, urls[i])
		}
	}
}

func TestCheckContextCancellationAbandonsRest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var firstDone = make(chan struct{}, 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		firstDone <- struct{}{}
	}))
	defer srv.Close()
	go func() {
		<-firstDone
		time.Sleep(10 * time.Millisecond) // let the first response finish being read
		cancel()
	}()

	const n = 20
	urls := make([]string, n)
	for i := range urls {
		urls[i] = srv.URL + "/l-" + strconv.Itoa(i)
	}

	start := time.Now()
	results := NewChecker(2, 0).Check(ctx, urls)
	elapsed := time.Since(start)

	if len(results) != n {
		t.Fatalf("len(results) = %d, want %d (every URL needs a result even when canceled)", len(results), n)
	}
	canceled := 0
	ok := 0
	for _, r := range results {
		switch {
		case r.Err == nil:
			ok++
		case errors.Is(r.Err, context.Canceled):
			canceled++
		default:
			t.Fatalf("unexpected error %v for %s", r.Err, r.URL)
		}
	}
	if ok == 0 || canceled == 0 {
		t.Fatalf("ok = %d, canceled = %d; want a partial result set", ok, canceled)
	}
	// Serial execution would need ~n*10ms; cancellation must cut it far below.
	if elapsed > 100*time.Millisecond {
		t.Errorf("elapsed = %v, want well under serial sum (~%v); cancellation did not abandon work", elapsed, n*10*time.Millisecond)
	}
}

func TestPerRequestTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // block until the test is done; timeout must fire first
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()    // runs second: waits for handlers to finish
	defer close(release) // runs first: unblocks the handler so Close can proceed

	start := time.Now()
	results := NewChecker(1, 50*time.Millisecond).Check(context.Background(), []string{srv.URL + "/slow"})
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	elapsed := time.Since(start)
	r := results[0]
	if r.Err == nil {
		t.Fatal("r.Err = nil, want timeout error")
	}
	if !errors.Is(r.Err, context.DeadlineExceeded) {
		t.Errorf("r.Err = %v, want it to wrap context.DeadlineExceeded", r.Err)
	}
	if r.Status != 0 {
		t.Errorf("r.Status = %d, want 0 when no response arrives", r.Status)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("request took %v, want ~50ms timeout to fire promptly", elapsed)
	}
}

func TestErrorTaxonomy(t *testing.T) {
	// Connection refused: a server that is already closed.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	// Status recorded without error.
	statusSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer statusSrv.Close()

	// Redirect chain: exactly three hops then a fourth redirect that must
	// NOT be followed. The last response (302 from hop three) is reported.
	var hitFinal int64
	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			atomic.AddInt64(&hitFinal, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, nextHop(r.URL.Path), http.StatusFound)
	}))
	defer redirectSrv.Close()

	c := NewChecker(3, 2*time.Second)
	ctx := context.Background()
	urls := []string{
		deadURL + "/refused",
		statusSrv.URL + "/missing",
		redirectSrv.URL + "/start",
	}
	results := c.Check(ctx, urls)
	if len(results) != len(urls) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(urls))
	}

	refused := results[0]
	if refused.Err == nil || refused.Status != 0 {
		t.Errorf("connection refused: got Status=%d Err=%v, want Status=0 with non-nil Err", refused.Status, refused.Err)
	}

	notFound := results[1]
	if notFound.Err != nil {
		t.Errorf("404 response: unexpected Err %v (status alone is not an error)", notFound.Err)
	}
	if notFound.Status != http.StatusNotFound {
		t.Errorf("404 response: Status = %d, want %d", notFound.Status, http.StatusNotFound)
	}

	redirected := results[2]
	if redirected.Err != nil {
		t.Errorf("redirect chain: unexpected Err %v", redirected.Err)
	}
	if redirected.Status != http.StatusFound {
		t.Errorf("redirect chain: Status = %d, want %d (stop after 3 redirects, report last response)", redirected.Status, http.StatusFound)
	}
	if n := atomic.LoadInt64(&hitFinal); n != 0 {
		t.Errorf("/final hit %d times, want 0 (never follow more than 3 redirects)", n)
	}
}

func nextHop(path string) string {
	hops := map[string]string{
		"/start": "/hop1",
		"/hop1":  "/hop2",
		"/hop2":  "/hop3",
		"/hop3":  "/final",
	}
	return hops[path]
}

func TestCheckRateLimitedSpacesRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const (
		n        = 6
		interval = 30 * time.Millisecond
	)
	urls := make([]string, n)
	for i := range urls {
		urls[i] = srv.URL + "/r-" + strconv.Itoa(i)
	}

	start := time.Now()
	results := NewChecker(n, 0).CheckRateLimited(context.Background(), urls, interval)
	elapsed := time.Since(start)

	if len(results) != n {
		t.Fatalf("len(results) = %d, want %d", len(results), n)
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("results[%d] unexpected error: %v", i, r.Err)
		}
		if r.URL != urls[i] {
			t.Errorf("results[%d].URL = %q, want %q", i, r.URL, urls[i])
		}
	}
	// n requests at one per interval tick need at least (n-1)*interval;
	// an unthrottled run would finish in well under half of that.
	if minElapsed := time.Duration(n-1) * interval; elapsed < minElapsed/2 {
		t.Errorf("elapsed = %v, want >= ~%v (requests were not rate limited)", elapsed, minElapsed)
	}
}

func TestInvalidWorkerCountFailsAtCheckTime(t *testing.T) {
	for _, workers := range []int{0, -3} {
		results := NewChecker(workers, time.Second).Check(
			context.Background(), []string{"http://127.0.0.1:1/a"},
		)
		if len(results) != 1 {
			t.Fatalf("workers=%d: len(results) = %d, want 1", workers, len(results))
		}
		r := results[0]
		if r.Err == nil {
			t.Errorf("workers=%d: Err = nil, want error", workers)
		}
		if r.Status != 0 {
			t.Errorf("workers=%d: Status = %d, want 0", workers, r.Status)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	results := NewChecker(2, time.Second).Check(context.Background(), nil)
	if len(results) != 0 {
		t.Errorf("len(results) = %d, want 0", len(results))
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	urls := make([]string, 20)
	for i := range urls {
		urls[i] = srv.URL + "/g-" + strconv.Itoa(i)
	}

	before := runtime.NumGoroutine()
	c := NewChecker(8, 0)
	c.Check(context.Background(), urls)
	c.CloseIdleConnections()
	time.Sleep(200 * time.Millisecond) // settle: let closed conns' goroutines exit

	if delta := runtime.NumGoroutine() - before; delta > 2 {
		t.Errorf("goroutines grew by %d after Check, want <= 2 (leaked workers or conns)", delta)
	}
}
