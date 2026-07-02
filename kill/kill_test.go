package kill

import (
	"errors"
	"strings"
	"testing"
)

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

func TestCmdErrFallsBackToGoError(t *testing.T) {
	// When the command produced no output, the Go error is the only
	// diagnostic and must be surfaced, not a bare "action: ".
	err := cmdErr("docker stop web", "", errors.New("exec: \"docker\": not found"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("cmdErr with empty output = %v, want it to include the Go error", err)
	}
	// When output IS present, prefer it.
	err = cmdErr("docker stop web", "  Error: no such container  ", errors.New("exit 1"))
	if err == nil || !strings.Contains(err.Error(), "no such container") {
		t.Errorf("cmdErr with output = %v, want it to include the command output", err)
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
