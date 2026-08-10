package tools

import (
	"errors"
	"strings"
	"testing"
)

// runSystem backs the flush-dns / restart-dock / restart-finder /
// sleep-display commands. We can't run the real ones in a test (they'd
// relaunch the Dock), but we can pin the helper's success/failure handling
// with harmless binaries.
//
// This is the one test in the package that deliberately opts back into real
// execution, because exit status IS what it is asserting and a neutered hook
// always reports success. The binaries are /usr/bin/true, /usr/bin/false and a
// path that does not exist — none of which can affect the machine.
func TestRunSystem(t *testing.T) {
	origCombined := runCombined
	defer func() { runCombined = origCombined }()
	runCombined = execCombined

	if err := runSystem("noop", "/usr/bin/true"); err != nil {
		t.Errorf("a zero-exit command should succeed, got %v", err)
	}
	if err := runSystem("noop", "/usr/bin/false"); err == nil {
		t.Error("a non-zero-exit command should return an error")
	}
	if err := runSystem("noop", "/nonexistent/xyzzy"); err == nil {
		t.Error("an unrunnable command should return an error")
	}
}

// system.go's whole contract is "run the SAME command the macOS app runs, so
// le is 1:1 with the GUI". That contract lives entirely in the argv, and until
// the exec hook existed there was no way to assert it without actually
// restarting the developer's Finder. Now there is.
func TestSystemToolsRunTheExactCommandsTheAppRuns(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func() error
		exe  string
		args []string
	}{
		{"FlushDNS", FlushDNS, "/usr/bin/dscacheutil", []string{"-flushcache"}},
		{"RestartDock", RestartDock, "/usr/bin/killall", []string{"Dock"}},
		{"RestartFinder", RestartFinder, "/usr/bin/killall", []string{"Finder"}},
		{"SleepDisplay", SleepDisplay, "/usr/bin/pmset", []string{"displaysleepnow"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			takeRecordedCommands() // isolate from earlier tests
			if err := tc.call(); err != nil {
				t.Fatalf("%s() = %v, want nil", tc.name, err)
			}
			got := takeRecordedCommands()
			if len(got) != 1 {
				t.Fatalf("%s ran %d commands, want exactly 1: %+v", tc.name, len(got), got)
			}
			if got[0].Exe != tc.exe {
				t.Errorf("exe = %q, want %q", got[0].Exe, tc.exe)
			}
			// Absolute paths on purpose: a bare "killall" would resolve
			// through PATH, and these run with the user's shell environment.
			if !strings.HasPrefix(got[0].Exe, "/") {
				t.Errorf("exe %q is not an absolute path", got[0].Exe)
			}
			if strings.Join(got[0].Args, " ") != strings.Join(tc.args, " ") {
				t.Errorf("args = %q, want %q", got[0].Args, tc.args)
			}
		})
	}
}

// A failure has to say what went wrong. The reason runSystem uses
// CombinedOutput at all is that the command's own message ("No matching
// processes belonging to you were found") is the useful part, and a bare
// "exit status 1" is not.
func TestRunSystemSurfacesTheCommandsOwnOutput(t *testing.T) {
	orig := runCombined
	defer func() { runCombined = orig }()
	runCombined = func(string, ...string) ([]byte, error) {
		return []byte("No matching processes belonging to you were found\n"), errors.New("exit status 1")
	}

	err := RestartDock()
	if err == nil {
		t.Fatal("RestartDock() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "No matching processes") {
		t.Errorf("error %q does not surface the command's output", err)
	}
	if !strings.Contains(err.Error(), "restart Dock") {
		t.Errorf("error %q does not name the operation", err)
	}
	if strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("error %q leaks the bare exit status the output was meant to replace", err)
	}
}

// When the command fails with no output there is nothing else to report, so
// the exec error is the fallback rather than an empty "failed: ".
func TestRunSystemFallsBackToTheExecErrorWhenOutputIsEmpty(t *testing.T) {
	orig := runCombined
	defer func() { runCombined = orig }()
	runCombined = func(string, ...string) ([]byte, error) {
		return nil, errors.New("fork/exec /usr/bin/pmset: permission denied")
	}

	err := SleepDisplay()
	if err == nil {
		t.Fatal("SleepDisplay() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error %q lost the exec failure", err)
	}
	if strings.HasSuffix(strings.TrimSpace(err.Error()), "failed:") {
		t.Errorf("error %q has an empty reason", err)
	}
}
