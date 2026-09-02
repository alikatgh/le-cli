package tools

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alikatgh/le-cli/scan"
)

// The two answers a liveness probe can give besides "alive". probeProcess
// (probe_unix.go / probe_windows.go) maps the platform's own errors onto
// these so WatchPID's logic is written once.
var (
	errProcessGone      = errors.New("process is gone")
	errProcessForbidden = errors.New("process belongs to another user")
)

// WatchPID blocks until process pid exits, probing once a second — the CLI
// half of the app's Notify-on-exit tool. It honours the same gone/forbidden
// distinction as ExitWatcher: gone means truly gone, forbidden means alive
// but owned by another user (so we can't reliably observe it). A positive
// timeout bounds the wait.
func WatchPID(pid int, timeout time.Duration) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	first := true
	for {
		err := probeProcess(pid)
		switch {
		case errors.Is(err, errProcessGone):
			if first {
				return fmt.Errorf("pid %d is not running", pid)
			}
			fmt.Printf("pid %d has exited\n", pid)
			return nil
		case errors.Is(err, errProcessForbidden):
			return fmt.Errorf("can't watch pid %d — it's owned by another user, so the OS won't report when it exits", pid)
		}
		// nil (alive) or any other errno (inconclusive) → keep polling.
		if first {
			fmt.Printf("watching pid %d — will report the moment it exits\n", pid)
			first = false
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			// ErrTimeout-wrapped so `le watch --timeout` exits 124 (still
			// running) rather than 1 (couldn't watch) — see tools.ErrTimeout.
			return fmt.Errorf("%w: pid %d still running after %s", ErrTimeout, pid, timeout)
		}
		time.Sleep(time.Second)
	}
}

// OpenWhenReady blocks until something is listening on port, then opens
// localhost:port/<path> in the browser (http or https auto-detected via a TLS
// probe, so vite --https / caddy links open correctly) — the app's
// Open-when-ready tool.
func OpenWhenReady(port, path string, timeout time.Duration) error {
	if err := WaitListening(port, timeout); err != nil {
		return err
	}
	url := scan.Scheme(port) + "://localhost:" + port + "/" + strings.TrimPrefix(path, "/")
	// Routed through the runToCompletion hook so the test binary can neuter it
	// — see exec_hooks.go. Injection safety is unchanged: the launcher is a
	// fixed command per platform (platform_*.go) and url is a discrete exec
	// argument, never a shell string.
	exe, args := browserCommand(url)
	if err := runToCompletion(exe, args...); err != nil {
		return fmt.Errorf("port is ready but couldn't open %s: %w", url, err)
	}
	fmt.Println("opened " + url)
	return nil
}
