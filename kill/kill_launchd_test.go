package kill

import (
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// LE-602: the StopLaunchd branch (launchctl bootout, with the label->PID
// recycle guard) was only covered indirectly. Pin the happy path and the
// refusal — labels, unlike PIDs, can be booted out and re-bootstrapped onto a
// different program between scan and stop.

func TestStopLaunchdHappyPath(t *testing.T) {
	defer withStubs(t)()
	orig := intel.LaunchdLabelPID
	defer func() { intel.LaunchdLabelPID = orig }()

	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	intel.LaunchdLabelPID = func(label string) (int, bool) { return 1, true } // still maps to the scanned PID

	var bootoutArg string
	runCombined = func(name string, args ...string) (string, error) {
		if name == "launchctl" && len(args) == 2 && args[0] == "bootout" {
			bootoutArg = args[1]
		}
		return "", nil
	}

	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopLaunchd, StopArg: "com.acme.agent"}
	if _, err := Stop(l, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(bootoutArg, "com.acme.agent") {
		t.Errorf("bootout target = %q, want it to contain the label", bootoutArg)
	}
}

func TestStopLaunchdRefusesRecycledLabel(t *testing.T) {
	defer withStubs(t)()
	orig := intel.LaunchdLabelPID
	defer func() { intel.LaunchdLabelPID = orig }()

	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	// The label now maps to a DIFFERENT pid than the one we scanned.
	intel.LaunchdLabelPID = func(label string) (int, bool) { return 999, true }
	runCombined = func(name string, args ...string) (string, error) {
		t.Fatal("must not bootout when the label no longer maps to the scanned pid")
		return "", nil
	}

	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopLaunchd, StopArg: "com.acme.agent"}
	if _, err := Stop(l, p); err == nil {
		t.Error("Stop should refuse a launchd label that was recycled since scan")
	}
}
