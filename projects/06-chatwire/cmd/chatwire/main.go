// Command chatwire is a terminal client for the chatwire TCP chat server.
//
// Usage: chatwire -addr host:port
//
// The first line typed becomes your display name; every later line is sent
// as a chat message. Incoming server lines print to stdout. Ctrl-D (EOF on
// stdin) exits cleanly.
package main

import (
	"flag"
	"fmt"
)

func main() {
	addr := flag.String("addr", ":6667", "server address to dial")
	flag.Parse()

	// TODO: Implement
	_ = addr
	_ = fmt.Sprint
}
