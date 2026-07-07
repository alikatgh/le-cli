package tools

import (
	"os"
	"testing"
	"time"
)

func TestWatchPIDNotRunning(t *testing.T) {
	// A PID above PID_MAX can't exist → ESRCH on the first probe → error.
	if err := WatchPID(999999, 0); err == nil {
		t.Error("watching a nonexistent pid should return an error")
	}
}

func TestWatchPIDTimesOut(t *testing.T) {
	// Our own process is alive, so a short timeout must elapse and report it
	// rather than hang forever.
	if err := WatchPID(os.Getpid(), 150*time.Millisecond); err == nil {
		t.Error("watching a live process past the timeout should error")
	}
}
