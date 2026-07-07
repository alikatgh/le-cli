package tools

import "testing"

func TestParsePortRange(t *testing.T) {
	cases := []struct {
		in     string
		lo, hi int
		ok     bool
	}{
		{"3000-3010", 3000, 3010, true},
		{"8080-8080", 8080, 8080, true},
		{"3000", 0, 0, false},        // single port, no dash — caller handles it
		{"notaport", 0, 0, false},    // garbage
		{"3010-3000", 0, 0, false},   // inverted
		{"0-100", 0, 0, false},       // lo < 1
		{"65000-70000", 0, 0, false}, // hi > 65535
		{"1-5000", 0, 0, false},      // too wide (>1024)
		{"3000-", 0, 0, false},       // missing hi
		{"-3000", 0, 0, false},       // missing lo
	}
	for _, c := range cases {
		lo, hi, ok := ParsePortRange(c.in)
		if ok != c.ok || (ok && (lo != c.lo || hi != c.hi)) {
			t.Errorf("ParsePortRange(%q) = (%d,%d,%v), want (%d,%d,%v)", c.in, lo, hi, ok, c.lo, c.hi, c.ok)
		}
	}
}
