// Command minikv runs the Redis-like key/value server.
//
//	Usage: minikv -addr :6380 -wal /tmp/minikv.wal -sync-every 1 \
//	             -max-memory 0 -policy lru
//
// Speak the RESP subset by hand, e.g.:
//
//	printf '*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n' | nc localhost 6380
//
// Ctrl-C or SIGTERM triggers a graceful Shutdown: accepting stops,
// connections drain, and the WAL is synced and closed.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	minikv "go101.dev/projects/10-minikv/solution"
)

func main() {
	addr := flag.String("addr", ":6380", "listen address")
	wal := flag.String("wal", "", "WAL file path (empty = in-memory only)")
	syncEvery := flag.Int("sync-every", 0, "fsync every N writes (1=durable per write, 0=batched by the OS)")
	maxMemory := flag.Int64("max-memory", 0, "memory cap in bytes (0 = unlimited)")
	policy := flag.String("policy", "none", "eviction policy: none | lru")
	flag.Parse()

	srv, err := minikv.NewServer(minikv.Config{
		Addr:           *addr,
		WALPath:        *wal,
		SyncEveryN:     *syncEvery,
		MaxMemory:      *maxMemory,
		EvictionPolicy: *policy,
	})
	if err != nil {
		log.Fatalf("minikv: %v", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case sig := <-sigCh:
		log.Printf("minikv: %v received; shutting down", sig)
		if err := srv.Shutdown(context.Background()); err != nil {
			log.Printf("minikv: shutdown: %v", err)
		}
	case err := <-errCh:
		if err != nil {
			log.Fatalf("minikv: %v", err)
		}
	}
}
