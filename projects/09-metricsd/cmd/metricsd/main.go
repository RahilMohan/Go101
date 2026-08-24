// Command metricsd runs the metrics scraper HTTP server.
//
// Usage: metricsd -addr :9090 -interval 15s -config targets.json
//
// targets.json is a JSON array of ScrapeTarget objects. GET /metrics
// serves the Prometheus text exposition format; GET /history/{name}
// returns the ring buffer for a target as a JSON array of Points.
// Ctrl-C triggers Scraper.Stop then a graceful server shutdown.
package main

import (
	"flag"
	"time"
)

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	interval := flag.Duration("interval", 15*time.Second, "scrape interval")
	scrapeTimeout := flag.Duration("scrape-timeout", 10*time.Second, "per-request timeout; bounds how long one hung target can delay the next tick")
	config := flag.String("config", "", "path to JSON array of ScrapeTarget")
	flag.Parse()

	// TODO: Implement
	_ = addr
	_ = interval
	_ = scrapeTimeout
	_ = config
}
