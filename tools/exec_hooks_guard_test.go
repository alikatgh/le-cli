package tools

import (
	"sync"
	"testing"
)

// Compiled ONLY into the test binary. Its init runs before any test in the
// package, so no test — including one written years from now by someone who
// has never read this file — can run `killall Finder`, `killall Dock`, `pmset
// displaysleepnow` or `open` against the developer's actual machine.
//
// This is the same guard shape as ui/launchers_guard_test.go, added for the
// same reason: LE-CLI-016, where per-call-site stubbing let a randomised test
// open ~876 real Terminal windows. The ui package learned it the expensive way;
// this package gets it before the accident rather than after.
//
// A test that wants to observe an invocation stubs the hook it cares about and
// reads recordedCommands — it just starts from a no-op instead of from a live
// exec.Command.
func init() {
	runCombined = func(exe string, args ...string) ([]byte, error) {
		recordCommand(exe, args)
		return nil, nil
	}
	runToCompletion = func(exe string, args ...string) error {
		recordCommand(exe, args)
		return nil
	}
}

type invocation struct {
	Exe  string
	Args []string
}

var (
	recordedMu       sync.Mutex
	recordedCommands []invocation
)

func recordCommand(exe string, args []string) {
	recordedMu.Lock()
	defer recordedMu.Unlock()
	recordedCommands = append(recordedCommands, invocation{Exe: exe, Args: args})
}

// A guard that silently stops being installed is worse than no guard, because
// the package's tests would go on passing while quietly running `killall
// Finder` on whoever ran them. This canary fails instead.
//
// /usr/bin/false is the probe precisely because it is harmless AND it cannot
// succeed: a nil error back from runCombined proves the hook is neutered,
// while the real implementation would return exit status 1. Same for
// runToCompletion.
func TestExecHooksAreNeuteredInTheTestBinary(t *testing.T) {
	takeRecordedCommands()

	if _, err := runCombined("/usr/bin/false"); err != nil {
		t.Fatalf("runCombined executed for real (got %v) — the init guard in this file is not installed, and this package's tests can now killall Finder", err)
	}
	if err := runToCompletion("/usr/bin/false"); err != nil {
		t.Fatalf("runToCompletion executed for real (got %v) — the init guard in this file is not installed, and this package's tests can now open windows", err)
	}

	if got := takeRecordedCommands(); len(got) != 2 {
		t.Fatalf("recorded %d invocations, want 2 — the recorder is not wired to both hooks: %+v", len(got), got)
	}
}

// takeRecordedCommands returns everything recorded since the last call and
// clears the log, so tests do not see each other's invocations.
func takeRecordedCommands() []invocation {
	recordedMu.Lock()
	defer recordedMu.Unlock()
	got := recordedCommands
	recordedCommands = nil
	return got
}
