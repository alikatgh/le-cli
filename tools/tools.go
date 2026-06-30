// Package tools implements the non-interactive port helpers — hold a port,
// wait for it to free up, wait for it to start listening — mirroring the macOS
// app's PortHolder / PortWatcher / PortFreedWatcher.
package tools

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const pollEvery = 400 * time.Millisecond

// Free reports whether 127.0.0.1:<port> can be bound right now — i.e. nothing
// is currently listening on it.
func Free(port string) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// Hold binds a sentinel listener on 127.0.0.1:<port> so nothing else can claim
// it, until the user interrupts.
func Hold(port string) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return fmt.Errorf("can't hold %s: %w", port, err)
	}
	defer ln.Close()

	// Accept and immediately drop connections so the socket stays healthy.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	fmt.Printf("holding 127.0.0.1:%s — press Ctrl-C to release\n", port)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Printf("\nreleased %s\n", port)
	return nil
}

// WaitFree blocks until 127.0.0.1:<port> is free.
func WaitFree(port string) error {
	if Free(port) {
		fmt.Printf("port %s is already free\n", port)
		return nil
	}
	fmt.Printf("waiting for port %s to free up… (Ctrl-C to stop)\n", port)
	waitUntil(func() bool { return Free(port) })
	fmt.Printf("✓ port %s is free\n", port)
	return nil
}

// WaitListening blocks until something starts listening on <port> — the
// "open when ready" primitive.
func WaitListening(port string) error {
	if !Free(port) {
		fmt.Printf("port %s is already listening\n", port)
		return nil
	}
	fmt.Printf("waiting for something to listen on %s… (Ctrl-C to stop)\n", port)
	waitUntil(func() bool { return !Free(port) })
	fmt.Printf("✓ port %s is now listening — http://localhost:%s/\n", port, port)
	return nil
}

func waitUntil(cond func() bool) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for range t.C {
		if cond() {
			return
		}
	}
}
