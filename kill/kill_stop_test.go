package kill

import (
	"errors"
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// withStubs saves the exec-indirection vars (and the two intel re-verify
// functions Stop depends on) and returns a restore func, so each test can
// stub only what it needs without leaking into the next.
func withStubs(t *testing.T) func() {
	t.Helper()
	oOut, oComb, oTerm := runOutput, runCombined, termProcess
	oBrew, oDocker := intel.BrewServiceKnown, intel.DockerContainerID
	return func() {
		runOutput, runCombined, termProcess = oOut, oComb, oTerm
		intel.BrewServiceKnown, intel.DockerContainerID = oBrew, oDocker
	}
}

// --- stillSame: the PID-recycle guard ---

func TestStillSameStartTimeMatch(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) {
		return "Mon Jun 23 14:00:00 2026\n", nil
	}
	l := scan.Listener{PID: 1, StartTime: "Mon Jun 23 14:00:00 2026"}
	if !stillSame(l) {
		t.Error("matching start time should be the same process")
	}
}

func TestStillSameToleratesLstartDoubleSpace(t *testing.T) {
	defer withStubs(t)()
	// Scan captured a single-space-normalized start time (via strings.Fields);
	// a fresh `ps -o lstart=` on a single-digit day pads with a second space.
	// These are the SAME instant and must still match.
	runOutput = func(name string, args ...string) (string, error) {
		return "Thu Jul  2 11:18:47 2026\n", nil // note the double space before "2"
	}
	l := scan.Listener{PID: 1, StartTime: "Thu Jul 2 11:18:47 2026"}
	if !stillSame(l) {
		t.Error("single- vs double-space in ps lstart must not read as a recycled PID")
	}
}

func TestStillSameStartTimeMismatchIsRecycled(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) {
		return "Tue Jun 24 09:00:00 2026\n", nil // a later start = recycled PID
	}
	l := scan.Listener{PID: 1, StartTime: "Mon Jun 23 14:00:00 2026"}
	if stillSame(l) {
		t.Error("a different start time means the PID was recycled — must not match")
	}
}

func TestStillSamePSErrorIsNotSame(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) {
		return "", errors.New("no such process")
	}
	l := scan.Listener{PID: 1, StartTime: "whenever"}
	if stillSame(l) {
		t.Error("a ps error (process gone) must not count as the same process")
	}
}

func TestStillSameCommandFallbackMatch(t *testing.T) {
	defer withStubs(t)()
	// No start time captured -> fall back to comparing the executable basename.
	runOutput = func(name string, args ...string) (string, error) {
		return "/usr/local/bin/node /app/server.js\n", nil
	}
	l := scan.Listener{PID: 1, CommandLine: "/usr/bin/node app.js"}
	if !stillSame(l) {
		t.Error("same exe basename via command fallback should match")
	}
}

func TestStillSameCommandFallbackMismatch(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) {
		return "/usr/bin/python3 other.py\n", nil
	}
	l := scan.Listener{PID: 1, CommandLine: "/usr/bin/node app.js"}
	if stillSame(l) {
		t.Error("different exe basename must not match")
	}
}

func TestStillSameEmptyCommandIsNotSame(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "\n", nil }
	l := scan.Listener{PID: 1, CommandLine: "/usr/bin/node"}
	if stillSame(l) {
		t.Error("empty ps command output must not match")
	}
}

// --- Stop: strategy branches ---

func TestStopTermSignalsWhenSame(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	var gotPID int
	termProcess = func(pid int) error { gotPID = pid; return nil }

	l := scan.Listener{PID: 4321, StartTime: "T"}
	msg, err := Stop(l, intel.Profile{StopKind: intel.StopTerm})
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if gotPID != 4321 {
		t.Errorf("termProcess got pid %d, want 4321", gotPID)
	}
	if !strings.Contains(msg, "4321") {
		t.Errorf("msg = %q, want it to mention the pid", msg)
	}
}

func TestStopRefusesRecycledPID(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "DIFFERENT\n", nil }
	termProcess = func(pid int) error {
		t.Fatal("must not signal a recycled PID")
		return nil
	}
	l := scan.Listener{PID: 4321, StartTime: "ORIGINAL"}
	if _, err := Stop(l, intel.Profile{StopKind: intel.StopTerm}); err == nil {
		t.Error("Stop should refuse a recycled PID")
	}
}

func TestStopAvoidRefuses(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	l := scan.Listener{PID: 1, StartTime: "T"}
	if _, err := Stop(l, intel.Profile{StopKind: intel.StopAvoid}); err == nil {
		t.Error("StopAvoid should return an error, not act")
	}
}

func TestStopBrewHappyPath(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	intel.BrewServiceKnown = func(formula string) bool { return true }
	var ranBrew bool
	runCombined = func(name string, args ...string) (string, error) {
		if name == "brew" {
			ranBrew = true
		}
		return "", nil
	}
	l := scan.Listener{PID: 1, StartTime: "T"}
	msg, err := Stop(l, intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"})
	if err != nil || !ranBrew || !strings.Contains(msg, "redis") {
		t.Errorf("brew happy path: msg=%q err=%v ranBrew=%v", msg, err, ranBrew)
	}
}

func TestStopBrewRefusesUnknownFormula(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	intel.BrewServiceKnown = func(formula string) bool { return false }
	runCombined = func(name string, args ...string) (string, error) {
		t.Fatal("must not run brew stop for an unknown formula")
		return "", nil
	}
	l := scan.Listener{PID: 1, StartTime: "T"}
	if _, err := Stop(l, intel.Profile{StopKind: intel.StopBrew, StopArg: "ghost"}); err == nil {
		t.Error("Stop should refuse a formula brew no longer knows")
	}
}

func TestStopBrewSurfacesCommandError(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	intel.BrewServiceKnown = func(formula string) bool { return true }
	runCombined = func(name string, args ...string) (string, error) {
		return "Error: service failed", errors.New("exit 1")
	}
	l := scan.Listener{PID: 1, StartTime: "T"}
	_, err := Stop(l, intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"})
	if err == nil || !strings.Contains(err.Error(), "service failed") {
		t.Errorf("expected the brew error surfaced, got %v", err)
	}
}

func TestStopDockerHappyPath(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	intel.DockerContainerID = func(name string) (string, bool) { return "abc123", true }
	var ranDocker bool
	runCombined = func(name string, args ...string) (string, error) {
		if name == "docker" {
			ranDocker = true
		}
		return "", nil
	}
	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopDocker, StopArg: "web", StopArgID: "abc123"}
	msg, err := Stop(l, p)
	if err != nil || !ranDocker || !strings.Contains(msg, "web") {
		t.Errorf("docker happy path: msg=%q err=%v ranDocker=%v", msg, err, ranDocker)
	}
}

func TestStopDockerRefusesChangedContainer(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	// Scan captured abc123, but the name now resolves to a different container.
	intel.DockerContainerID = func(name string) (string, bool) { return "def456", true }
	runCombined = func(name string, args ...string) (string, error) {
		t.Fatal("must not docker stop a container whose ID changed since scan")
		return "", nil
	}
	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopDocker, StopArg: "web", StopArgID: "abc123"}
	if _, err := Stop(l, p); err == nil {
		t.Error("Stop should refuse a container whose ID changed since scan")
	}
}
