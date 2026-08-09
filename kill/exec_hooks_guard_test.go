package kill

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// Compiled ONLY into the test binary, so its init runs before any test in the
// package and no test can send a real signal or run a real `launchctl bootout`
// / `brew services stop` / `docker stop`.
//
// The hooks in kill.go were already vars — the author indirected them so tests
// could exercise Stop's branches — but every stub was per-test. That is the
// arrangement ui/ had before LE-CLI-016, where a randomised test pressed keys
// the stubs did not cover and opened ~876 real windows. Here the uncovered path
// is syscall.Kill against a PID from test data, which on an unlucky value
// signals something real; the ones next to it tear down launchd services and
// stop containers.
//
// So: default to refusing, loudly. A test that wants to observe a strategy
// stubs the hook it cares about, exactly as the existing ones do — the
// save/restore helpers in kill_stop_test.go keep working, they simply restore
// to a refusal instead of to live exec.
//
// Refusing with an ERROR rather than a silent success is deliberate. Stop's
// logic branches on these errors, so a fake success would let a test assert
// "stopped cleanly" down a path that in production would have shelled out. An
// error can only ever make a test louder.
var errTestGuard = errors.New("kill: test binary refused to run this for real (see exec_hooks_guard_test.go)")

func init() {
	runCombined = func(name string, args ...string) (string, error) {
		return "", guardErr(name, args)
	}
	runOutput = func(name string, args ...string) (string, error) {
		return "", guardErr(name, args)
	}
	termProcess = func(pid int) error {
		return errors.New("kill: test binary refused to SIGTERM pid " + strconv.Itoa(pid) + " (see exec_hooks_guard_test.go)")
	}
}

func guardErr(name string, args []string) error {
	return errors.New(errTestGuard.Error() + ": " + name + " " + strings.Join(args, " "))
}

// A guard that silently stops being installed is worse than no guard: the
// tests keep passing while quietly signalling real processes. /usr/bin/true
// is the probe because the real implementation would SUCCEED on it, so an
// error back proves the hook is neutered.
func TestExecHooksAreNeuteredInTheTestBinary(t *testing.T) {
	if _, err := runCombined("/usr/bin/true"); err == nil {
		t.Fatal("runCombined executed for real — the init guard in this file is not installed, and this package's tests can now run launchctl/brew/docker against the machine")
	}
	if _, err := runOutput("/usr/bin/true"); err == nil {
		t.Fatal("runOutput executed for real — the init guard in this file is not installed")
	}
	// PID 0 is the probe: syscall.Kill(0, SIGTERM) signals the caller's whole
	// process group, so the real implementation would take the test binary
	// down with it. Getting an error back instead is the proof.
	if err := termProcess(0); err == nil {
		t.Fatal("termProcess executed for real — the init guard in this file is not installed, and this package's tests can now signal real processes")
	}
}
