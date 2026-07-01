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

func TestSameExe(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/usr/bin/node app.js --port 3000", "/usr/bin/node app.js", true},
		{"/usr/bin/node", "/usr/local/bin/node", true},
		{"/usr/bin/python3 -m http.server", "/usr/bin/node", false},
		{"", "/usr/bin/node", false},
	}
	for _, c := range cases {
		if got := sameExe(c.a, c.b); got != c.want {
			t.Errorf("sameExe(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestArgv0Base(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/node app.js --port 3000": "node",
		"":                                 "",
		"   ":                              "",
		"relative/path/to/bin arg1":        "bin",
	}
	for in, want := range cases {
		if got := argv0Base(in); got != want {
			t.Errorf("argv0Base(%q) = %q, want %q", in, got, want)
		}
	}
}
