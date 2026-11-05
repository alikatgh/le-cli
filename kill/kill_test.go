package kill

import (
	"errors"
	"strings"
	"testing"
)

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

func TestLaunchdGuardOK(t *testing.T) {
	cases := []struct {
		name            string
		scanPID, curPID int
		lookupOK, want  bool
	}{
		{"same pid", 42, 42, true, true},
		{"label re-bootstrapped to new pid", 42, 99, true, false},
		{"label gone", 42, 0, false, false},
	}
	for _, c := range cases {
		if got := launchdGuardOK(c.scanPID, c.curPID, c.lookupOK); got != c.want {
			t.Errorf("%s: launchdGuardOK(%d, %d, %v) = %v, want %v", c.name, c.scanPID, c.curPID, c.lookupOK, got, c.want)
		}
	}
}
