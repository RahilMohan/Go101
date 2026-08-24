// Command metricsd runs the metrics scraper HTTP server.
//
// Usage: metricsd -addr :9090 -interval 15s -config targets.json
//
// targets.json is a JSON array of ScrapeTarget objects, e.g.:
//
//	[
//	  {"name": "pool", "url": "http://localhost:8081/stats", "jsonPath": "workers.active"}
//	]
//
// GET /metrics serves the Prometheus text exposition format;
// GET /history/{name} returns the ring buffer for a target as a JSON
// array of Points. Ctrl-C or SIGTERM triggers Scraper.Stop then a
// graceful server shutdown.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go101.dev/projects/09-metricsd/solution"
)

func main() {
	addr := flag.String("addr", ":9090", "listen address")
	interval := flag.Duration("interval", 15*time.Second, "scrape interval")
	scrapeTimeout := flag.Duration("scrape-timeout", 10*time.Second, "per-request timeout; bounds how long one hung target can delay the next tick")
	config := flag.String("config", "", "path to JSON array of ScrapeTarget")
	flag.Parse()

	if *config == "" {
		log.Fatalf("no config given: pass -config <path to a JSON array of ScrapeTarget>")
	}
	raw, err := os.ReadFile(*config)
	if err != nil {
		log.Fatalf("read config: %v", err)
	}
	var targets []metricsd.ScrapeTarget
	if err := json.Unmarshal(raw, &targets); err != nil {
		log.Fatalf("parse config: %v", err)
	}
	seen := make(map[string]bool, len(targets))
	for _, tg := range targets {
		if tg.Name == "" || seen[tg.Name] {
			log.Fatalf("config: duplicate or empty target name %q", tg.Name)
		}
		seen[tg.Name] = true
	}

	collector := metricsd.New()
	client := &http.Client{Timeout: *scrapeTimeout} // DefaultClient has no timeout: one stalled body would freeze all scraping forever
	scraper := metricsd.NewScraper(collector, client)
	scraper.Start(context.Background(), targets, *interval)

	server := &http.Server{
		Addr:    *addr,
		Handler: metricsd.Handler(collector, scraper),
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		scraper.Stop() // no new samples after this
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("metricsd listening on %s (interval %s, %d targets)", *addr, *interval, len(targets))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
	scraper.Stop()
}
