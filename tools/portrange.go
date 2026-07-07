package tools

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// ParsePortRange parses "3000-3010" into (3000, 3010, true). Returns ok=false
// for a single port / non-range (no dash — the caller handles those), an
// invalid endpoint, an inverted range, or an absurdly wide one (>1024 ports) —
// bounded so a typo can't try to bind thousands of sockets.
func ParsePortRange(s string) (lo, hi int, ok bool) {
	loStr, hiStr, found := strings.Cut(s, "-")
	if !found {
		return 0, 0, false
	}
	l, err1 := strconv.Atoi(strings.TrimSpace(loStr))
	h, err2 := strconv.Atoi(strings.TrimSpace(hiStr))
	if err1 != nil || err2 != nil || l < 1 || h > 65535 || l > h || h-l > 1024 {
		return 0, 0, false
	}
	return l, h, true
}

// HoldRange binds a sentinel listener on every port in [lo, hi] so nothing else
// can grab any of them, until the user interrupts. Ports already in use are
// skipped (holding the rest is still useful) and reported. Mirrors Hold.
func HoldRange(lo, hi int) error {
	var held []net.Listener
	var busy int
	for p := lo; p <= hi; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			busy++
			continue
		}
		held = append(held, ln)
		go func(l net.Listener) {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}(ln)
	}
	defer func() {
		for _, l := range held {
			_ = l.Close()
		}
	}()

	if len(held) == 0 {
		return fmt.Errorf("couldn't hold any port in %d-%d (all in use)", lo, hi)
	}
	fmt.Printf("holding %d port(s) in %d-%d", len(held), lo, hi)
	if busy > 0 {
		fmt.Printf(" (skipped %d already in use)", busy)
	}
	fmt.Println(" — press Ctrl-C to release")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Printf("\nreleased %d port(s)\n", len(held))
	return nil
}
