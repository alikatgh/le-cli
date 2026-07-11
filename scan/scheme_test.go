package scan

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestSchemeDetectsTLSAndCaches(t *testing.T) {
	var calls atomic.Int32
	restore := SetTLSProbeForTesting(func(port string) bool { calls.Add(1); return true })
	defer restore()

	if got := Scheme("8443"); got != "https" {
		t.Fatalf("Scheme = %q, want https", got)
	}
	if got := Scheme("8443"); got != "https" {
		t.Fatalf("second Scheme = %q, want https", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("probe ran %d times, want 1 (cached)", n)
	}

	restore2 := SetTLSProbeForTesting(func(port string) bool { return false })
	defer restore2()
	if got := Scheme("3000"); got != "http" {
		t.Fatalf("Scheme(plain) = %q, want http", got)
	}
}

func TestCachedSchemeNeverBlocksAndWarms(t *testing.T) {
	probed := make(chan struct{}, 8)
	restore := SetTLSProbeForTesting(func(port string) bool {
		select {
		case probed <- struct{}{}:
		default:
		}
		return true
	})
	defer restore()

	// Miss: returns http immediately, probes in the background.
	if got := CachedScheme("8443"); got != "http" {
		t.Fatalf("cold CachedScheme = %q, want http placeholder", got)
	}
	select {
	case <-probed:
	case <-time.After(2 * time.Second):
		t.Fatal("background probe never ran")
	}

	// The warmed cache serves https on a later call (poll: the goroutine
	// stores after signalling).
	deadline := time.Now().Add(2 * time.Second)
	for CachedScheme("8443") != "https" {
		if time.Now().After(deadline) {
			t.Fatal("cache never warmed to https")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCachedSchemeStaleReturnsLastKnown(t *testing.T) {
	restore := SetTLSProbeForTesting(func(port string) bool { return true })
	defer restore()

	// Seed a STALE https entry.
	schemeMu.Lock()
	schemeCache["8443"] = schemeEntry{scheme: "https", at: time.Now().Add(-time.Minute)}
	schemeMu.Unlock()

	if got := CachedScheme("8443"); got != "https" {
		t.Fatalf("stale CachedScheme = %q, want last-known https", got)
	}
}
