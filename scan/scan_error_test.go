//go:build !windows

package scan

import (
	"os/exec"
	"testing"
)

// LE-CLI-002 / LE-CLI-014: a scan that fails because lsof cannot run must not
// be indistinguishable from "nothing is listening". lsof exits non-zero on an
// empty match, so a non-zero *exit* is the normal empty case — but a failure to
// start lsof at all is a real error that has to surface, or `le stop` answers
// "nothing listening" on a broken machine.

func TestScanSurfacesExecFailure(t *testing.T) {
	orig := runCmd
	defer func() { runCmd = orig }()
	// lsof couldn't be started at all (missing binary) -> *exec.Error, which is
	// NOT an *exec.ExitError. This must surface as an error.
	runCmd = func(name string, args ...string) (string, error) {
		return "", &exec.Error{Name: "lsof", Err: exec.ErrNotFound}
	}
	got, err := Scan()
	if err == nil {
		t.Errorf("Scan() with unrunnable lsof = (%v, nil), want an error", got)
	}
}

func TestScanExitNonZeroEmptyIsNoListeners(t *testing.T) {
	orig := runCmd
	defer func() { runCmd = orig }()
	// lsof ran and exited non-zero with no output — its normal "no matches"
	// signal. That is the legitimate empty case, not a failure.
	runCmd = func(name string, args ...string) (string, error) {
		return "", &exec.ExitError{}
	}
	got, err := Scan()
	if err != nil || got != nil {
		t.Errorf("Scan() with empty non-zero lsof = (%v, %v), want (nil, nil)", got, err)
	}
}
