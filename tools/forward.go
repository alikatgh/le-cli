package tools

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Forward supervises a `kubectl port-forward` session with auto-reconnect —
// the thing raw kubectl doesn't do: a dropped cluster connection kills the
// forward silently and you find out when your requests start failing. This
// wrapper restarts it with backoff until Ctrl-C, and timestamps every
// connect/drop so there's a visible history.
//
// Args pass through to kubectl verbatim after `port-forward`: target
// (pod/deploy/svc), port mappings, and flags like -n/--context.
//
// Package vars below are seams for tests: startForward launches the real
// kubectl; sleeps are real time.
var (
	startForward = func(args []string) (wait func() error, kill func(), err error) {
		// #nosec G204 -- "kubectl" is a fixed binary name resolved via PATH and
		// args are discrete exec arguments (no shell interpretation).
		c := exec.Command("kubectl", append([]string{"port-forward"}, args...)...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			return nil, nil, err
		}
		return c.Wait, func() { _ = c.Process.Kill() }, nil
	}
	forwardInitialBackoff = time.Second
	forwardMaxBackoff     = 15 * time.Second
	// A run that survives this long counts as "was healthy" — the next drop
	// starts the backoff ladder from the bottom instead of where it left off.
	forwardHealthyAfter = 30 * time.Second
)

// Forward blocks, supervising the forward until the user interrupts.
func Forward(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("give kubectl port-forward arguments, e.g. `le forward svc/frontend 8080:80 -n prod`")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	backoff := forwardInitialBackoff
	fmt.Printf("forwarding: kubectl port-forward %s — Ctrl-C to stop\n", strings.Join(args, " "))

	for attempt := 1; ; attempt++ {
		started := time.Now()
		wait, kill, err := startForward(args)
		if err != nil {
			return fmt.Errorf("could not start kubectl: %w (is kubectl installed and on PATH?)", err)
		}
		fmt.Printf("[%s] forward up (session %d)\n", time.Now().Format("15:04:05"), attempt)

		done := make(chan error, 1)
		go func() { done <- wait() }()

		select {
		case <-sig:
			kill()
			<-done
			fmt.Printf("\n[%s] stopped\n", time.Now().Format("15:04:05"))
			return nil
		case runErr := <-done:
			// kubectl exited on its own — connection drop, pod restart, or an
			// immediate error (bad target, no cluster). Reconnect with backoff;
			// a healthy run resets the ladder.
			if time.Since(started) >= forwardHealthyAfter {
				backoff = forwardInitialBackoff
			}
			reason := "exited"
			if runErr != nil {
				reason = runErr.Error()
			}
			fmt.Printf("[%s] forward dropped (%s) — reconnecting in %s (Ctrl-C to stop)\n",
				time.Now().Format("15:04:05"), reason, backoff)

			select {
			case <-sig:
				fmt.Printf("\n[%s] stopped\n", time.Now().Format("15:04:05"))
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > forwardMaxBackoff {
				backoff = forwardMaxBackoff
			}
		}
	}
}
