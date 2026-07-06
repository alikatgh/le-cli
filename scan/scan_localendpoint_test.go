package scan

import "testing"

// LE-796/LE-779: `le` must list only localhost-reachable listeners, matching the
// mac app. Loopback (127.x, [::1]) and wildcard binds (*, 0.0.0.0, [::]) stay; a
// bind to a specific LAN IP or a hostname is dropped. Kept prefix-for-prefix in
// step with LocalhostScanner.isLocalEndpoint.
func TestIsLocalEndpoint(t *testing.T) {
	keep := []string{
		"127.0.0.1:3000",
		"127.0.0.53:53",
		"[::1]:8080",
		"localhost:5432",
		"*:3000",
		"0.0.0.0:5173",
		"[::]:9229",
	}
	drop := []string{
		"192.168.1.5:8080",
		"10.0.0.2:3000",
		"172.16.0.1:80",
		"[fe80::1]:8080",
		"example.com:443",
		"",
	}
	for _, a := range keep {
		if !isLocalEndpoint(a) {
			t.Errorf("isLocalEndpoint(%q) = false, want true (localhost/wildcard bind)", a)
		}
	}
	for _, a := range drop {
		if isLocalEndpoint(a) {
			t.Errorf("isLocalEndpoint(%q) = true, want false (not a localhost listener)", a)
		}
	}
}
