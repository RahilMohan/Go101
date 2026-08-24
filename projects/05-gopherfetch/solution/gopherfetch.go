// Package solution is the reference implementation of project 05
// (gopherfetch): a concurrent link checker. The lessons are goroutine
// lifecycle (a fixed pool of workers draining an index channel),
// sync.WaitGroup for join semantics, context propagation for
// cancellation, per-request timeouts derived from a parent context, rate
// limiting with a ticker, and deterministic output: results land at the
// index of their input URL, so callers always get input order no matter
// what order requests complete in.
package solution

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// ErrNoWorkers is reported on every Result when Check (or
// CheckRateLimited) is called on a Checker built with workers <= 0.
var ErrNoWorkers = errors.New("gopherfetch: worker count must be positive")

// maxRedirects caps how many redirects are followed before reporting the
// last response as-is (link checkers care about "is this link OK", not
// about chasing arbitrary redirect chains).
const maxRedirects = 3

// Result is the outcome of checking a single URL.
type Result struct {
	URL      string        // as requested
	Status   int           // HTTP status; 0 if the request failed before a response
	Err      error         // nil on success
	Duration time.Duration // per-request wall time
}

// Checker fetches URLs with a bounded worker pool over a shared client.
type Checker struct {
	workers int
	timeout time.Duration
	client  *http.Client
}

// NewChecker returns a Checker running at most `workers` goroutines
// (workers <= 0 -> error at Check time). Each in-flight request gets a
// per-request timeout derived from timeout (<= 0 means no per-request
// timeout).
func NewChecker(workers int, timeout time.Duration) *Checker {
	return &Checker{
		workers: workers,
		timeout: timeout,
		client: &http.Client{
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return http.ErrUseLastResponse // report the 3xx instead of following hop four
				}
				return nil
			},
			Transport: &http.Transport{
				// Idle-connection pooling is per host; sizing it to the
				// pool avoids churn when every worker hits the same host.
				MaxIdleConns:        workers,
				MaxIdleConnsPerHost: workers,
			},
		},
	}
}

// CloseIdleConnections shuts down pooled keep-alive connections so tests
// (and long-lived processes) can observe a quiesced goroutine count.
func (c *Checker) CloseIdleConnections() { c.client.CloseIdleConnections() }

// Check fetches every URL exactly once using the pool. ctx cancellation
// stops scheduling new requests and abandons the rest: results for unsent
// URLs carry ctx.Err(). Results are ordered exactly as the input URLs.
func (c *Checker) Check(ctx context.Context, urls []string) []Result {
	return c.run(ctx, urls, 0)
}

// CheckRateLimited is the rate-limited variant of Check: after the first
// request, at most one request begins every `interval` tick. interval <= 0
// behaves like Check.
func (c *Checker) CheckRateLimited(ctx context.Context, urls []string, interval time.Duration) []Result {
	return c.run(ctx, urls, interval)
}

// run is the shared engine: dispatch indices to a fixed worker pool,
// optionally pacing dispatches on a ticker, then join and repair any
// results that cancellation left unfilled.
func (c *Checker) run(ctx context.Context, urls []string, interval time.Duration) []Result {
	results := make([]Result, len(urls))
	if c.workers <= 0 {
		for i := range results {
			results[i] = Result{URL: urls[i], Err: ErrNoWorkers}
		}
		return results
	}

	n := len(urls)
	if n == 0 {
		return results
	}

	// Workers own disjoint indices of results, so they never contend on a
	// slice element; wg.Wait below gives the happens-before edge that lets
	// the dispatcher read every slot safely afterward.
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(min(c.workers, n))
	for range min(c.workers, n) {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				results[idx] = c.doOne(ctx, urls[idx])
			}
		}()
	}

	var tick <-chan time.Time
	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C
	}

dispatch:
	for idx := range n {
		if tick != nil && idx > 0 {
			select {
			case <-ctx.Done():
				break dispatch
			case <-tick:
			}
		}
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- idx:
		}
	}
	close(jobs) // workers drain what was already sent, then exit
	wg.Wait()

	if err := ctx.Err(); err != nil {
		for i := range results {
			if results[i].URL == "" { // never picked up by a worker
				results[i] = Result{URL: urls[i], Err: err}
			}
		}
	}
	return results
}

// doOne issues one HEAD request and converts it into a Result. The request
// context derives from ctx, so both caller cancellation and the optional
// per-request timeout reach the transport.
func (c *Checker) doOne(ctx context.Context, url string) Result {
	reqCtx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	start := time.Now()
	res := Result{URL: url}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, url, nil)
	if err != nil {
		res.Duration = time.Since(start)
		res.Err = err
		return res
	}
	resp, err := c.client.Do(req)
	res.Duration = time.Since(start)
	if err != nil {
		res.Err = err // connection refused, timeout, canceled context, ...
		return res
	}
	resp.Body.Close()
	res.Status = resp.StatusCode // non-2xx is data, not an error
	return res
}
