// Command chatwire is a terminal client for the chatwire TCP chat server.
//
// Usage: chatwire -addr host:port
//
// The first line typed becomes your display name; every later line is sent
// as a chat message. Incoming server lines print to stdout. Ctrl-D (EOF on
// stdin) exits cleanly.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"

	"go101.dev/projects/06-chatwire/solution"
)

func main() {
	addr := flag.String("addr", ":6667", "server address to dial")
	flag.Parse()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chatwire:", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Fprintln(os.Stderr, "connected — first line is your display name; Ctrl-D quits")

	// Printer goroutine: everything the server sends goes to stdout.
	printerDone := make(chan struct{})
	go func() {
		defer close(printerDone)
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			fmt.Println(sc.Text())
		}
	}()

	// Main loop: one stdin line per wire line; EOF exits cleanly.
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := in.Text()
		if line == "" {
			continue
		}
		if err := solution.WriteLine(conn, line); err != nil {
			break
		}
	}
	conn.Close() // unblocks the printer with an error → it exits
	<-printerDone
}
