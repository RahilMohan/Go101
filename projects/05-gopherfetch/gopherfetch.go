// Package gopherfetch provides a concurrent link checker, teaching
// goroutine lifecycle, bounded worker pools, sync.WaitGroup, context
// cancellation, rate limiting, and deterministic concurrency testing.
//
// Security note (teaching scope): Checker performs no SSRF filtering and
// follows redirects (up to maxRedirects) to any host. A public URL that
// redirects to a private IP (loopback, 10/8, 169.254.169.254, link-local,
// etc.) can launder probes to internal networks via the redirect follower.
// This is an intentional teaching-scope limitation, not a production-
// hardened fetcher. Production deployments must sanitize URLs or add a
// DialContext filter that rejects private/link-local ranges before use.
package gopherfetch

import (
	"context"
	"errors"
	"io"
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

// maxWorkers caps the worker pool and idle-connection pool to bound
// goroutines and file descriptors. Callers requesting more workers are
// silently clamped; workers <= 0 still surfaces as ErrNoWorkers at Check
// time. The cap is documented here and in NewChecker.
const maxWorkers = 256

// Result is the outcome of checking a single URL.
type Result struct {
	URL      string        // as requested
	Status   int           // HTTP status; 0 if the request failed before a response
	Err      error         // nil on success
	Duration time.Duration // per-request wall time
}

// Checker fetches URLs with a bounded worker pool.
type Checker struct {
	workers int
	timeout time.Duration
	client  *http.Client
}

// NewChecker returns a Checker running at most `workers` goroutines
// (workers <= 0 -> error at Check time). Each in-flight request gets a
// per-request timeout derived from timeout (<= 0 means no per-request
// timeout). workers is capped to maxWorkers (256) to bound goroutines and
// idle connections; larger values are clamped. Workers > 1000 would also be
// clamped to the same cap (effectively capped, not unbounded).
func NewChecker(workers int, timeout time.Duration) *Checker {
	idleConns := workers
	if idleConns < 0 {
		idleConns = 0
	}
	if idleConns > maxWorkers {
		idleConns = maxWorkers
	}
	return &Checker{
		workers: workers,
		timeout: timeout,
		client: &http.Client{
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) > maxRedirects {
					return http.ErrUseLastResponse // report the 3xx instead of following hop four
				}
				return nil
			},
			Transport: &http.Transport{
				// Idle-connection pooling is per host; sizing it to the
				// pool avoids churn when every worker hits the same host.
				// Capped to maxWorkers; negative workers yields 0 (default).
				MaxIdleConns:        idleConns,
				MaxIdleConnsPerHost: idleConns,
			},
		},
	}
}

// Check fetches every URL exactly once using the pool. ctx cancellation
// stops scheduling new requests and abandons the rest: results for unsent
// URLs carry ctx.Err(). Results are returned as a slice ordered EXACTLY as
// the input URLs. Check must not leak goroutines.
func (c *Checker) Check(ctx context.Context, urls []string) []Result {
	return c.run(ctx, urls, 0)
}

// CheckRateLimited is the rate-limited variant of Check: at most one
// request begins every `interval` tick.
func (c *Checker) CheckRateLimited(ctx context.Context, urls []string, interval time.Duration) []Result {
	return c.run(ctx, urls, interval)
}

// CloseIdleConnections shuts down pooled keep-alive connections.
func (c *Checker) CloseIdleConnections() { c.client.CloseIdleConnections() }

// run is the shared engine: dispatch indices to a fixed worker pool,
// optionally pacing dispatches on a ticker, then join and repair any
// results that cancellation left unfilled.
func (c *Checker) run(ctx context.Context, urls []string, interval time.Duration) []Result {
	results := make([]Result, len(urls))
	if ctx == nil {
		err := errors.New("gopherfetch: nil context")
		for i := range results {
			results[i] = Result{URL: urls[i], Err: err}
		}
		return results
	}
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

	workers := c.workers
	if workers > maxWorkers {
		workers = maxWorkers
	}

	// Workers own disjoint indices of results, so they never contend on a
	// slice element; wg.Wait below gives the happens-before edge that lets
	// the dispatcher read every slot safely afterward.
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(min(workers, n))
	for range min(workers, n) {
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

	// filled[i] records that index i was handed to a worker; written only
	// by the dispatcher, so no synchronization is needed. Repairing unfilled
	// slots after cancellation keys off this marker rather than any zero
	// value of Result, which a legitimate result could collide with.
	filled := make([]bool, n)

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
			filled[idx] = true
		}
	}
	close(jobs) // workers drain what was already sent, then exit
	wg.Wait()

	if err := ctx.Err(); err != nil {
		for i := range results {
			if !filled[i] { // never scheduled, so never checked
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
	if ctx == nil {
		ctx = context.Background()
	}
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
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	res.Status = resp.StatusCode // non-2xx is data, not an error
	return res
}
