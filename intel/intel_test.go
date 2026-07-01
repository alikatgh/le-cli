package intel

import "testing"

// Regression test for the audit finding: two containers transiently
// publishing the same port used to silently overwrite each other in the
// port->name map, which kill.Stop would then act on. A collision should now
// be dropped rather than guessed.
func TestParseDockerPortsCollisionIsDropped(t *testing.T) {
	out := "abc123\tweb\t0.0.0.0:3000->3000/tcp\ndef456\tweb2\t0.0.0.0:3000->3000/tcp\n"
	got := parseDockerPorts(out)
	if _, ok := got["3000"]; ok {
		t.Errorf("colliding port should be dropped, got %v", got)
	}
}

func TestParseDockerPortsNormal(t *testing.T) {
	out := "abc123\tweb\t0.0.0.0:3000->3000/tcp\n"
	got := parseDockerPorts(out)
	c, ok := got["3000"]
	if !ok || c.name != "web" || c.id != "abc123" {
		t.Errorf("got %v, want {web abc123}", got)
	}
}

func TestParseDockerPortsMultiplePorts(t *testing.T) {
	out := "abc123\tweb\t0.0.0.0:3000->3000/tcp, 0.0.0.0:8080->8080/tcp\n"
	got := parseDockerPorts(out)
	if got["3000"].name != "web" || got["8080"].name != "web" {
		t.Errorf("got %v", got)
	}
}

func TestParseDockerPortsSameContainerTwiceIsNotAmbiguous(t *testing.T) {
	// The same container can legitimately appear to publish the same port
	// twice (e.g. IPv4 and IPv6 lines) — that's not a collision.
	out := "abc123\tweb\t0.0.0.0:3000->3000/tcp\nabc123\tweb\t[::]:3000->3000/tcp\n"
	got := parseDockerPorts(out)
	if got["3000"].name != "web" {
		t.Errorf("same-container repeat should not be dropped, got %v", got)
	}
}

func TestWordMatchBoundaries(t *testing.T) {
	cases := []struct {
		haystack, needle string
		want             bool
	}{
		{"/usr/bin/node", "node", true},
		{"node app.js", "node", true},
		{"nodebox", "node", false},
		{"node_modules", "node", false},
		{"a-node-b", "node", true},
	}
	for _, c := range cases {
		if got := wordMatch(c.haystack, c.needle); got != c.want {
			t.Errorf("wordMatch(%q, %q) = %v, want %v", c.haystack, c.needle, got, c.want)
		}
	}
}

func TestBrewFormulaSkipsBareHomebrewSegment(t *testing.T) {
	if f := brewFormula("/opt/homebrew/opt/mongodb-community/bin/mongod"); f != "mongodb-community" {
		t.Errorf("got %q, want mongodb-community", f)
	}
	if f := brewFormula("/opt/homebrew/bin/le"); f != "" {
		t.Errorf("got %q, want empty (not a Cellar/opt path)", f)
	}
}
