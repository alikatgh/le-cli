package kill

import "testing"

// TestDockerGuardOK exercises the docker container-recycling guard in
// isolation, so a future refactor that inverts its condition (silently
// defeating the protection it exists to provide) fails a test instead of
// shipping.
func TestDockerGuardOK(t *testing.T) {
	cases := []struct {
		name      string
		scanArgID string
		curID     string
		lookupOK  bool
		want      bool
	}{
		{"no id captured at scan time - nothing to check, proceed", "", "", false, true},
		{"ids match, lookup succeeded - proceed", "abc123", "abc123", true, true},
		{"ids differ, lookup succeeded - refuse (container was replaced)", "abc123", "def456", true, false},
		{"lookup failed (removed or ambiguous name) - refuse", "abc123", "", false, false},
		{"lookup ok but empty curID - refuse", "abc123", "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dockerGuardOK(c.scanArgID, c.curID, c.lookupOK); got != c.want {
				t.Errorf("dockerGuardOK(%q, %q, %v) = %v, want %v", c.scanArgID, c.curID, c.lookupOK, got, c.want)
			}
		})
	}
}

func TestNormalizeWS(t *testing.T) {
	cases := map[string]string{
		"Thu Jul  2 11:18:47 2026": "Thu Jul 2 11:18:47 2026",
		"  spaced   out  ":         "spaced out",
		"":                         "",
	}
	for in, want := range cases {
		if got := normalizeWS(in); got != want {
			t.Errorf("normalizeWS(%q) = %q, want %q", in, got, want)
		}
	}
}
