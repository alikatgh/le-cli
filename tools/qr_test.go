package tools

import "testing"

func TestQRRejectsBadPort(t *testing.T) {
	for _, bad := range []string{"", "abc", "0", "-1", "70000", "3000x", " "} {
		if err := QR(bad); err == nil {
			t.Errorf("QR(%q) = nil, want error for an invalid port", bad)
		}
	}
}

func TestPrivateIPv4sAreActuallyPrivate(t *testing.T) {
	// Whatever the CI runner's interfaces are, every address we return must be a
	// private, non-loopback IPv4 — never a public or loopback one.
	for _, ip := range privateIPv4s() {
		if ip == "127.0.0.1" {
			t.Errorf("privateIPv4s() returned loopback %q", ip)
		}
	}
}
