// Command minikv runs the Redis-like key/value server.
//
//	Usage: minikv -addr :6380 -wal /tmp/minikv.wal -sync-every 1 \
//	             -max-memory 0 -policy lru
//
// Speak the RESP subset by hand, e.g.:
//
//	printf '*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n' | nc localhost 6380
package main

import (
	"flag"
)

func main() {
	addr := flag.String("addr", ":6380", "listen address")
	wal := flag.String("wal", "", "WAL file path (empty = in-memory only)")
	syncEvery := flag.Int("sync-every", 0, "fsync every N writes (1=durable per write, 0=batched by the OS)")
	maxMemory := flag.Int64("max-memory", 0, "memory cap in bytes (0 = unlimited)")
	policy := flag.String("policy", "none", "eviction policy: none | lru")
	flag.Parse()

	// TODO: Implement
	_ = addr
	_ = wal
	_ = syncEvery
	_ = maxMemory
	_ = policy
}
