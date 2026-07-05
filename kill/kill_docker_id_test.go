package kill

import (
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// LE-060: `docker stop` must target the immutable container ID, not the
// reassignable name. The recycle guard confirms name->ID at check time, but
// executing by name still leaves a TOCTOU window where a freed name could be
// grabbed by a different container before the stop lands.

func TestStopDockerStopsByImmutableID(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	intel.DockerContainerID = func(name string) (string, bool) { return "abc123", true }

	var stopArg string
	runCombined = func(name string, args ...string) (string, error) {
		if name == "docker" && len(args) == 2 && args[0] == "stop" {
			stopArg = args[1]
		}
		return "", nil
	}

	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopDocker, StopArg: "web", StopArgID: "abc123"}
	if _, err := Stop(l, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopArg != "abc123" {
		t.Errorf("docker stop targeted %q, want the immutable ID abc123 — stopping by name risks killing a different container that reused the name", stopArg)
	}
}

func TestStopDockerFallsBackToNameWhenNoID(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	// No ID captured at scan time: the guard is a no-op and there is nothing
	// immutable to target, so stopping by name is the only option.
	intel.DockerContainerID = func(name string) (string, bool) { return "", false }

	var stopArg string
	runCombined = func(name string, args ...string) (string, error) {
		if name == "docker" && len(args) == 2 && args[0] == "stop" {
			stopArg = args[1]
		}
		return "", nil
	}

	l := scan.Listener{PID: 1, StartTime: "T"}
	p := intel.Profile{StopKind: intel.StopDocker, StopArg: "web", StopArgID: ""}
	if _, err := Stop(l, p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopArg != "web" {
		t.Errorf("with no ID captured, docker stop should fall back to the name; got %q", stopArg)
	}
}
