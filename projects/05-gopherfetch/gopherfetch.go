// Package gopherfetch provides a concurrent link checker, teaching
// goroutine lifecycle, bounded worker pools, sync.WaitGroup, context
// cancellation, rate limiting, and deterministic concurrency testing.
package gopherfetch

import (
	"context"
	"time"
)

// Result is the outcome of checking a single URL.
type Result struct {
	URL      string        // as requested
	Status   int           // HTTP status; 0 if the request failed before a response
	Err      error         // nil on success
	Duration time.Duration // per-request wall time
}

// Checker fetches URLs with a bounded worker pool.
type Checker struct {
	// TODO: Implement
	_ struct{}
}

// NewChecker returns a Checker running at most `workers` goroutines
// (workers <= 0 -> error at Check time). Each in-flight request gets a
// per-request timeout derived from timeout (<= 0 means no per-request
// timeout).
func NewChecker(workers int, timeout time.Duration) *Checker {
	// TODO: Implement
	return nil
}

// Check fetches every URL exactly once using the pool. ctx cancellation
// stops scheduling new requests and abandons the rest: results for unsent
// URLs carry ctx.Err(). Results are returned as a slice ordered EXACTLY as
// the input URLs. Check must not leak goroutines.
func (c *Checker) Check(ctx context.Context, urls []string) []Result {
	// TODO: Implement
	return nil
}

// CheckRateLimited is the rate-limited variant of Check: at most one
// request begins every `interval` tick.
func (c *Checker) CheckRateLimited(ctx context.Context, urls []string, interval time.Duration) []Result {
	// TODO: Implement
	return nil
}

// CloseIdleConnections shuts down pooled keep-alive connections.
func (c *Checker) CloseIdleConnections() {
	// TODO: Implement
}
