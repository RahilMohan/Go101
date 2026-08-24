// Command shorty runs the shorty URL shortener HTTP server.
//
// Usage: shorty -addr :8080 -db ./shorty.db -base http://localhost:8080
//
// Ctrl-C triggers a graceful shutdown (5s deadline).
package main

import (
	"flag"
	"fmt"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	db := flag.String("db", "./shorty.db", "SQLite database path")
	base := flag.String("base", "http://localhost:8080", "base URL used in short_url responses")
	flag.Parse()

	// TODO: Implement
	_ = addr
	_ = db
	_ = base
	_ = fmt.Sprint
}
