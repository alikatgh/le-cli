package kill

import (
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// CLI parity: `le restart` bounces a verified brew listener through its owner
// (with the same identity guards as Stop); a plain process has no supervisor,
// so restart refuses rather than guess.

func TestRestartBrewBouncesFormula(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil } // stillSame guard
	intel.BrewServiceKnown = func(formula string) bool { return true }
	intel.LaunchdLabelPID = func(label string) (int, bool) { return 1, true }

	var got []string
	runCombined = func(name string, args ...string) (string, error) {
		got = append([]string{name}, args...)
		return "", nil
	}
	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"}
	if _, err := Restart(l, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 || got[0] != "brew" || got[1] != "services" || got[2] != "restart" || got[3] != "redis" {
		t.Errorf("ran %v, want [brew services restart redis]", got)
	}
}

func TestRestartRefusesPlainProcess(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopTerm, Identity: "node server.js"}
	if _, err := Restart(l, p); err == nil {
		t.Error("a plain process has no supervisor — restart must refuse, not guess")
	}
}
