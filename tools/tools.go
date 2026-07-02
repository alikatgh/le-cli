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
	defer func() { _ = ln.Close() }()

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

// WaitFree blocks until 127.0.0.1:<port> is free. A positive timeout bounds
// the wait and returns an error if it elapses first; timeout <= 0 waits
// indefinitely (Ctrl-C to stop).
func WaitFree(port string, timeout time.Duration) error {
	if Free(port) {
		fmt.Printf("port %s is already free\n", port)
		return nil
	}
	fmt.Printf("waiting for port %s to free up… (%s)\n", port, waitHint(timeout))
	if !waitUntil(func() bool { return Free(port) }, timeout) {
		return fmt.Errorf("timed out after %s waiting for port %s to free", timeout, port)
	}
	fmt.Printf("✓ port %s is free\n", port)
	return nil
}

// WaitListening blocks until something starts listening on <port> — the
// "open when ready" primitive. See WaitFree for timeout semantics.
func WaitListening(port string, timeout time.Duration) error {
	if !Free(port) {
		fmt.Printf("port %s is already listening\n", port)
		return nil
	}
	fmt.Printf("waiting for something to listen on %s… (%s)\n", port, waitHint(timeout))
	if !waitUntil(func() bool { return !Free(port) }, timeout) {
		return fmt.Errorf("timed out after %s waiting for a listener on port %s", timeout, port)
	}
	fmt.Printf("✓ port %s is now listening — http://localhost:%s/\n", port, port)
	return nil
}

func waitHint(timeout time.Duration) string {
	if timeout > 0 {
		return "up to " + timeout.String() + ", Ctrl-C to stop"
	}
	return "Ctrl-C to stop"
}

// waitUntil polls cond every pollEvery until it returns true (→ true) or, when
// timeout > 0, until the deadline elapses first (→ false). A non-positive
// timeout means no deadline: it blocks until cond is met.
func waitUntil(cond func() bool, timeout time.Duration) bool {
	if cond() {
		return true
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	var deadline <-chan time.Time
	if timeout > 0 {
		deadline = time.After(timeout)
	}
	for {
		select {
		case <-t.C:
			if cond() {
				return true
			}
		case <-deadline:
			// One final check AT the deadline. Without it, a timeout shorter
			// than pollEvery (e.g. -t 200ms) always fired before the first
			// tick and returned false even if cond had already become true.
			return cond()
		}
	}
}
