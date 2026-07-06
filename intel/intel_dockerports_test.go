package intel

import "testing"

// R69: a ranged Docker publication (0.0.0.0:8000-8010->8000-8010/tcp) was
// dropped by the old single-port regex, so the loopback listener wasn't
// attributed to its container and `le stop` recommended a raw TERM on the
// docker-proxy instead of `docker stop`. Expand the host-side range, matching
// the Swift app's publishedPorts.
func TestParseDockerPortsExpandsRanges(t *testing.T) {
	m := parseDockerPorts("abc123\tweb\t0.0.0.0:8000-8010->8000-8010/tcp")
	for _, p := range []string{"8000", "8005", "8010"} {
		if c, ok := m[p]; !ok || c.name != "web" {
			t.Errorf("port %s not attributed to web (got %+v, ok=%v)", p, c, ok)
		}
	}
	if _, ok := m["7999"]; ok {
		t.Error("7999 must not be attributed (below the range)")
	}
	if _, ok := m["8011"]; ok {
		t.Error("8011 must not be attributed (above the range)")
	}
}

func TestParseDockerPortsSinglePortStillWorks(t *testing.T) {
	m := parseDockerPorts("id1\tapi\t0.0.0.0:3000->3000/tcp")
	if c, ok := m["3000"]; !ok || c.name != "api" {
		t.Errorf("single port 3000 not attributed (got %+v, ok=%v)", c, ok)
	}
}

func TestParseDockerPortsDropsAbsurdRange(t *testing.T) {
	// A range wider than 1024 is dropped entirely (as in Swift), not expanded.
	m := parseDockerPorts("id2\tbig\t0.0.0.0:1000-9000->1000-9000/tcp")
	if len(m) != 0 {
		t.Errorf("an absurdly wide range must be dropped, got %d entries", len(m))
	}
}
