// Command shorty runs the shorty URL shortener HTTP server.
//
// Usage: shorty -addr :8080 -db ./shorty.db -base http://localhost:8080
//
// Ctrl-C triggers a graceful shutdown (5s deadline).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	shorty "go101.dev/projects/07-shorty/solution"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	db := flag.String("db", "./shorty.db", "SQLite database path")
	base := flag.String("base", "http://localhost:8080", "base URL used in short_url responses")
	flag.Parse()

	store, err := shorty.OpenStore(*db)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	srv := &http.Server{
		Addr:    *addr,
		Handler: shorty.NewServer(store, *base).Handler(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Printf("shorty listening on %s (db %s, base %s)\n", *addr, *db, *base)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
