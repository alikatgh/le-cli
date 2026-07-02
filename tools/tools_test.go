package tools

import (
	"net"
	"testing"
	"time"
)

func TestFreeReflectsBinding(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // OS picks a free port
	if err != nil {
		t.Skipf("cannot bind a test port: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	if Free(port) {
		t.Fatalf("port %s is bound but Free() reported it free", port)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !Free(port) {
		t.Errorf("port %s was released but Free() reported it busy", port)
	}
}

// freePort grabs an OS-assigned port, then releases it — giving a port number
// that was free a moment ago. On loopback in a test this is reliable enough;
// the tiny reuse window doesn't matter for these assertions.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a test port: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	return port
}

// runWithin fails the test if fn doesn't return within d — a guard so a
// mis-taken fast path (which would otherwise block on waitUntil forever)
// surfaces as a failure instead of a hung test.
func runWithin(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("call did not return in time (unexpected block)")
	}
}

func TestWaitFreeReturnsImmediatelyWhenFree(t *testing.T) {
	port := freePort(t)
	runWithin(t, 2*time.Second, func() {
		if err := WaitFree(port, 0); err != nil {
			t.Errorf("WaitFree(%s) = %v, want nil", port, err)
		}
	})
}

func TestWaitListeningReturnsImmediatelyWhenListening(t *testing.T) {
	port := freePort(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Skipf("setup bind failed: %v", err)
	}
	defer func() { _ = ln.Close() }()
	runWithin(t, 2*time.Second, func() {
		if err := WaitListening(port, 0); err != nil {
			t.Errorf("WaitListening(%s) = %v, want nil", port, err)
		}
	})
}

func TestHoldFailsWhenPortTaken(t *testing.T) {
	port := freePort(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Skipf("setup bind failed: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if err := Hold(port); err == nil {
		t.Errorf("Hold(%s) = nil on an already-bound port, want an error", port)
	}
}

func TestWaitUntilReturnsWhenCondBecomesTrue(t *testing.T) {
	calls := 0
	runWithin(t, 3*time.Second, func() {
		if !waitUntil(func() bool {
			calls++
			return calls >= 2 // true on the second poll
		}, 0) {
			t.Error("waitUntil returned false without a timeout")
		}
	})
	if calls < 2 {
		t.Errorf("waitUntil polled %d times, want at least 2", calls)
	}
}

func TestWaitUntilTimesOut(t *testing.T) {
	// A cond that never becomes true must return false once the timeout
	// elapses, rather than blocking forever.
	start := time.Now()
	var timedOut bool
	runWithin(t, 3*time.Second, func() {
		timedOut = !waitUntil(func() bool { return false }, 600*time.Millisecond)
	})
	if !timedOut {
		t.Error("waitUntil with an unsatisfiable cond should time out (return false)")
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Errorf("returned after %s, want it to honor the ~600ms deadline", elapsed)
	}
}

func TestWaitFreeTimesOutOnBusyPort(t *testing.T) {
	port := freePort(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Skipf("setup bind failed: %v", err)
	}
	defer func() { _ = ln.Close() }()
	var gotErr error
	runWithin(t, 3*time.Second, func() {
		gotErr = WaitFree(port, 500*time.Millisecond) // never frees
	})
	if gotErr == nil {
		t.Error("WaitFree on a permanently-busy port with a timeout should return an error")
	}
}

// The real reason `le wait` exists: block on a busy port, then return once it
// frees. Exercises the polling path, not just the already-free fast path.
func TestWaitFreeUnblocksWhenPortFrees(t *testing.T) {
	port := freePort(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Skipf("setup bind failed: %v", err)
	}
	// Release the port shortly after WaitFree starts polling.
	go func() {
		time.Sleep(pollEvery + 100*time.Millisecond)
		_ = ln.Close()
	}()
	runWithin(t, 5*time.Second, func() {
		if err := WaitFree(port, 0); err != nil {
			t.Errorf("WaitFree = %v, want nil once the port freed", err)
		}
	})
}

// The real reason `le ready` exists: block until something starts listening.
func TestWaitListeningUnblocksWhenSomethingListens(t *testing.T) {
	port := freePort(t)
	// The listener is handed back over a channel, not a shared var — the
	// channel receive gives the happens-before that keeps -race quiet.
	lnCh := make(chan net.Listener, 1)
	go func() {
		time.Sleep(pollEvery + 100*time.Millisecond)
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
		if err != nil {
			lnCh <- nil
			return
		}
		lnCh <- l
	}()
	runWithin(t, 5*time.Second, func() {
		if err := WaitListening(port, 0); err != nil {
			t.Errorf("WaitListening = %v, want nil once something listened", err)
		}
	})
	if ln := <-lnCh; ln != nil {
		_ = ln.Close()
	}
}
