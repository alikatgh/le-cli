package kill

import (
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// CLI parity: `le restart` bounces a brew/docker listener through its owner
// (with the same identity guards as Stop); a plain process has no supervisor,
// so restart refuses rather than guess.

func TestRestartBrewBouncesFormula(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil } // stillSame guard
	intel.BrewServiceKnown = func(formula string) bool { return true }

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

func TestRestartDockerByImmutableID(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	intel.DockerContainerID = func(name string) (string, bool) { return "abc123", true }

	var target string
	runCombined = func(name string, args ...string) (string, error) {
		if name == "docker" && len(args) == 2 && args[0] == "restart" {
			target = args[1]
		}
		return "", nil
	}
	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopDocker, StopArg: "web", StopArgID: "abc123"}
	if _, err := Restart(l, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "abc123" {
		t.Errorf("docker restart targeted %q, want the immutable ID abc123 (stopping by name risks bouncing a different container)", target)
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
