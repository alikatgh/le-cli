package scan

import (
	"crypto/tls"
	"net"
	"sync"
	"time"
)

// Scheme detection: is the dev server on this port speaking HTTPS or plain
// HTTP? vite --https, caddy, and friends serve TLS on localhost, and opening
// http:// against them gets a broken page — so `o` in the TUI, `le open`, and
// the table's clickable links all ask here first.
//
// Detection = attempt a TLS handshake. We dial "localhost" (not 127.0.0.1) so
// Go's dual-stack dialing also reaches IPv6-only listeners bound to [::1].
// Certificate verification is deliberately skipped: dev certs are self-signed,
// and we only detect that TLS is spoken — nothing is transmitted or trusted.

const (
	schemeProbeTimeout = 350 * time.Millisecond
	schemeCacheTTL     = 30 * time.Second
)

var (
	schemeMu    sync.Mutex
	schemeCache = map[string]schemeEntry{}
	// probeTLS is a package var so tests can stub the network away.
	probeTLS = func(port string) bool {
		d := net.Dialer{Timeout: schemeProbeTimeout}
		// #nosec G402 -- InsecureSkipVerify is required and safe here: this is
		// TLS *detection* against localhost dev servers with self-signed certs.
		// No request is sent and nothing about the connection is trusted.
		conn, err := tls.DialWithDialer(&d, "tcp", net.JoinHostPort("localhost", port), &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         "localhost",
		})
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
)

type schemeEntry struct {
	scheme string
	at     time.Time
}

// Scheme returns "https" or "http" for a local port, probing (and caching) if
// needed. Blocks up to schemeProbeTimeout on a cache miss — fine on a keypress
// or after WaitListening, NOT fine in a render loop (use CachedScheme there).
func Scheme(port string) string {
	schemeMu.Lock()
	if e, ok := schemeCache[port]; ok && time.Since(e.at) < schemeCacheTTL {
		schemeMu.Unlock()
		return e.scheme
	}
	schemeMu.Unlock()
	return refreshScheme(port)
}

// CachedScheme is the render-path variant: it never blocks. A fresh cache hit
// returns the known scheme; a stale hit returns the last-known scheme while
// re-probing in the background; a miss returns "http" immediately and warms
// the cache, so the next repaint (the TUI redraws every scan tick) shows the
// corrected scheme.
func CachedScheme(port string) string {
	schemeMu.Lock()
	e, ok := schemeCache[port]
	fresh := ok && time.Since(e.at) < schemeCacheTTL
	if !fresh {
		// Stamp now so concurrent renders don't stack duplicate probes; the
		// goroutine overwrites with the real answer.
		known := "http"
		if ok {
			known = e.scheme
		}
		schemeCache[port] = schemeEntry{scheme: known, at: time.Now()}
	}
	schemeMu.Unlock()

	if fresh {
		return e.scheme
	}
	go func() { _ = refreshScheme(port) }()
	if ok {
		return e.scheme // stale but last-known beats a guess
	}
	return "http"
}

// SetTLSProbeForTesting swaps the TLS prober and clears the scheme cache so
// cross-package tests (ui, tools) stay hermetic — no real dials. Returns a
// restore func. Test hook only; not for production use.
func SetTLSProbeForTesting(probe func(port string) bool) (restore func()) {
	schemeMu.Lock()
	orig := probeTLS
	probeTLS = probe
	schemeCache = map[string]schemeEntry{}
	schemeMu.Unlock()
	return func() {
		schemeMu.Lock()
		probeTLS = orig
		schemeCache = map[string]schemeEntry{}
		schemeMu.Unlock()
	}
}

// refreshScheme force-probes and stores; returns the fresh scheme. The prober
// is read under the lock so the test hook can swap it without racing the
// background goroutines CachedScheme spawns.
func refreshScheme(port string) string {
	schemeMu.Lock()
	probe := probeTLS
	schemeMu.Unlock()

	s := "http"
	if probe(port) {
		s = "https"
	}
	schemeMu.Lock()
	schemeCache[port] = schemeEntry{scheme: s, at: time.Now()}
	schemeMu.Unlock()
	return s
}
