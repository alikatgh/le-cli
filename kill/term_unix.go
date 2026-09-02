//go:build !windows

package kill

import (
	"strconv"
	"syscall"
)

// SIGTERM is the graceful stop on every unix: the process gets to run its
// handlers and exit on its own terms. termProcess is a var so the test-binary
// guard (exec_hooks_guard_test.go) can neuter it.
var termProcess = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func termCommand(pid int) string { return "kill -TERM " + strconv.Itoa(pid) }
func termResult(pid int) string  { return "sent SIGTERM to " + strconv.Itoa(pid) }

// The identity re-reads behind stillSame. Both go through runOutput so the
// existing tests, which stub runOutput with canned ps output, keep pinning
// the guard's behaviour. lstart is the recycle key; see stillSame for the
// whitespace hazard its comparison has to absorb.
func readStartTime(pid int) (string, error) {
	return runOutput("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
}

func readCommandLine(pid int) (string, error) {
	return runOutput("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=")
}
