package tools

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// KeepAwake runs `caffeinate -d -i` (prevent display + idle sleep) — the exact
// invocation the app's Keep-awake tool uses — blocking until the duration
// elapses or the user interrupts. A zero duration keeps the Mac awake until
// Ctrl-C. The interrupt is relayed so caffeinate stops immediately.
func KeepAwake(d time.Duration) error {
	args := []string{"-d", "-i"}
	if d > 0 {
		args = append(args, "-t", strconv.Itoa(int(d.Seconds())))
		fmt.Printf("keeping awake for %s — Ctrl-C to stop early\n", d)
	} else {
		fmt.Println("keeping awake until you press Ctrl-C")
	}

	c := exec.Command("/usr/bin/caffeinate", args...)
	if err := c.Start(); err != nil {
		return fmt.Errorf("could not start caffeinate: %w", err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()

	select {
	case <-sig:
		_ = c.Process.Kill()
		<-done
		fmt.Println("\nstopped — your Mac can sleep again")
	case <-done:
		fmt.Println("time's up — your Mac can sleep again")
	}
	return nil
}
