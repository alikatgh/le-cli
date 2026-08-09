package textw

import "testing"

// The two failures that motivated this package, as tests: a CJK app name in a
// fixed-width column, and a multi-port cell wider than its column.
func TestCellHoldsItsWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"ascii short", "adb", 12},
		{"ascii exact", "abcdefghijkl", 12},
		{"ascii long", "Grammarly Desktop Helper", 12},
		{"cjk", "企业微信", 12},                // 4 runes, 8 columns
		{"cjk overflowing", "企业微信企业微信", 6}, // must clip by columns, not runes
		{"emoji", "my🚀app", 12},
		{"multi-port cell", "*44950 +1", 9},
		{"empty", "", 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Width(Cell(c.in, c.n)); got != c.n {
				t.Errorf("Cell(%q, %d) is %d columns wide, want exactly %d", c.in, c.n, got, c.n)
			}
		})
	}
}

func TestTruncateNeverExceedsBudget(t *testing.T) {
	for _, n := range []int{1, 2, 3, 8, 20} {
		for _, s := range []string{"企业微信企业微信", "Grammarly Desktop", "my🚀app🚀here", "adb"} {
			if got := Width(Truncate(s, n)); got > n {
				t.Errorf("Truncate(%q, %d) is %d columns wide", s, n, got)
			}
		}
	}
}

func TestPadLeavesOversizedStringsAlone(t *testing.T) {
	// Pad must not silently clip — callers that need clipping use Cell, and a
	// surprise truncation inside Pad would hide the overflow instead of the
	// column visibly breaking.
	in := "a much longer string"
	if got := Pad(in, 4); got != in {
		t.Errorf("Pad clipped: %q", got)
	}
}
