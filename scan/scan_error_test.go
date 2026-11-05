//go:build !windows

package scan

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestScanSurfacesExecFailure(t *testing.T) {
	orig := runCmd
	defer func() { runCmd = orig }()
	runCmd = func(name string, args ...string) (string, error) {
		return "", &exec.Error{Name: "lsof", Err: exec.ErrNotFound}
	}
	got, err := Scan()
	if got != nil || !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("Scan() with unrunnable lsof = (%v, %v), want (nil, ErrNotFound)", got, err)
	}
}

// Use a real ProcessState and captured Stderr: an empty exec.ExitError has no
// exit status and cannot represent lsof's normal exit-1 no-match result.
func lsofExitError(t *testing.T, code int, diagnostic string) *exec.ExitError {
	t.Helper()
	// #nosec G204 -- Fixed shell text; synthetic diagnostics and exit codes are passed as discrete arguments, never interpolated.
	cmd := exec.Command("/bin/sh", "-c", `printf '%s' "$1" >&2; exit "$2"`, "lsof-fixture", diagnostic, strconv.Itoa(code))
	_, err := cmd.Output()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != code || string(exitErr.Stderr) != diagnostic {
		t.Fatalf("fixture exit = %v, want status %d and captured diagnostic", err, code)
	}
	return exitErr
}

func TestScanLsofExitStatus(t *testing.T) {
	const partial = "p1234\ncnode\nn127.0.0.1:3000\n"
	const diagnostic = "fixture diagnostic with private process data"
	cases := []struct {
		name    string
		code    int
		stdout  string
		stderr  string
		wantErr bool
	}{
		{"no matches", 1, "", "", false},
		{"exit 1 diagnostic", 1, "", diagnostic, true},
		{"exit 2 silent", 2, "", "", true},
		{"exit 2 diagnostic", 2, "", diagnostic, true},
		{"partial exit 1 silent", 1, partial, "", true},
		{"partial exit 1 diagnostic", 1, partial, diagnostic, true},
		{"partial exit 2 silent", 2, partial, "", true},
		{"partial exit 2 diagnostic", 2, partial, diagnostic, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exitErr := lsofExitError(t, tc.code, tc.stderr)
			orig := runCmd
			defer func() { runCmd = orig }()
			runCmd = func(name string, args ...string) (string, error) {
				return tc.stdout, exitErr
			}
			got, err := Scan()
			if (err != nil) != tc.wantErr || got != nil {
				t.Fatalf("Scan() = (%v, %v), want (nil, error=%v)", got, err, tc.wantErr)
			}
			if tc.wantErr && (!errors.Is(err, exitErr) || !strings.Contains(err.Error(), "lsof:")) {
				t.Errorf("Scan error = %v, want wrapped lsof exit error", err)
			}
			if err != nil && strings.Contains(err.Error(), diagnostic) {
				t.Error("Scan error exposed collector diagnostic data")
			}
		})
	}
}
