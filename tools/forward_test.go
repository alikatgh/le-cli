//go:build !windows

package tools

import (
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestForwardRequiresArgs(t *testing.T) {
	if err := Forward(nil); err == nil {
		t.Error("Forward with no args should error, not launch kubectl")
	}
}

func TestForwardReconnectsAfterDropAndStopsOnInterrupt(t *testing.T) {
	origStart, origBackoff := startForward, forwardInitialBackoff
	defer func() { startForward, forwardInitialBackoff = origStart, origBackoff }()
	forwardInitialBackoff = time.Millisecond

	var launches atomic.Int32
	startForward = func(args []string) (func() error, func(), error) {
		n := launches.Add(1)
		if n == 1 {
			// First session drops immediately — Forward must reconnect.
			return func() error { return errors.New("connection lost") }, func() {}, nil
		}
		// Later sessions block until killed (the Ctrl-C path).
		ch := make(chan struct{})
		var closed atomic.Bool
		kill := func() {
			if closed.CompareAndSwap(false, true) {
				close(ch)
			}
		}
		return func() error { <-ch; return nil }, kill, nil
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT) // caught by Forward's Notify
	}()

	if err := Forward([]string{"svc/x", "8080:80"}); err != nil {
		t.Fatalf("Forward = %v, want clean stop on interrupt", err)
	}
	if launches.Load() < 2 {
		t.Fatalf("launches = %d, want >= 2 (must reconnect after the drop)", launches.Load())
	}
}

func TestForwardSurfacesLaunchFailure(t *testing.T) {
	origStart := startForward
	defer func() { startForward = origStart }()
	startForward = func(args []string) (func() error, func(), error) {
		return nil, nil, errors.New("kubectl: executable file not found")
	}
	err := Forward([]string{"svc/x", "8080:80"})
	if err == nil {
		t.Fatal("a kubectl launch failure must surface, not loop forever")
	}
}
