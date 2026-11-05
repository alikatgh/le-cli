package kill

import (
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// A name or even a full ID says nothing about engine locality or ownership.
// Legacy profiles must never re-query or mutate an ambient Docker context.
func TestDockerProfilesAlwaysRequireManualOwnershipVerification(t *testing.T) {
	for _, fixture := range []struct{ name, id, host string }{
		{"missing ID", "", ""},
		{"short ID", "abc123", ""},
		{"full ID", strings.Repeat("a", 64), "unix:///var/run/docker.sock"},
		{"remote engine", strings.Repeat("a", 64), "ssh://production"},
		{"forwarded Unix socket", strings.Repeat("a", 64), "unix:///tmp/forwarded-docker.sock"},
		{"stale or ambiguous target", "old-or-ambiguous", "tcp://remote:2376"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			defer withStubs(t)()
			t.Setenv("DOCKER_HOST", fixture.host)
			t.Setenv("DOCKER_CONTEXT", "local-before-scan")
			runOutput = func(name string, args ...string) (string, error) {
				t.Fatal("Docker refusal must not query an engine or PID")
				return "", nil
			}
			runCombined = func(name string, args ...string) (string, error) {
				t.Fatal("must not execute any manager command")
				return "", nil
			}
			termProcess = func(pid int) error { t.Fatal("must not fall back to a raw signal"); return nil }
			l := scan.Listener{PID: 1, StartTime: "T"}
			p := intel.Profile{StopKind: intel.StopDocker, StopArg: "web", StopArgID: fixture.id}
			t.Setenv("DOCKER_CONTEXT", "remote-after-scan")
			for name, action := range map[string]func(scan.Listener, intel.Profile) (string, error){"stop": Stop, "restart": Restart} {
				msg, err := action(l, p)
				if msg != "" || err == nil || !strings.Contains(err.Error(), "ownership are unverified") {
					t.Fatalf("%s: msg=%q err=%v", name, msg, err)
				}
			}
		})
	}
}

func TestKnownDockerHelperNeverFallsBackToRawSignal(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) {
		t.Fatal("manual helper must refuse before queries")
		return "", nil
	}
	runCombined = func(name string, args ...string) (string, error) {
		t.Fatal("manual helper must not invoke a manager")
		return "", nil
	}
	termProcess = func(pid int) error { t.Fatal("must not signal a Docker helper"); return nil }
	for _, kind := range []intel.StopKind{intel.StopTerm, intel.StopAvoid, intel.StopBrew, intel.StopLaunchd, ""} {
		l := scan.Listener{PID: 1, Command: "docker-proxy", CommandLine: "/usr/bin/docker-proxy -host-port 3000"}
		for _, action := range []func(scan.Listener, intel.Profile) (string, error){Stop, Restart} {
			if _, err := action(l, intel.Profile{StopKind: kind}); err == nil || !strings.Contains(err.Error(), "ownership are unverified") {
				t.Fatalf("helper strategy %q: %v", kind, err)
			}
		}
	}
}

func TestUnrelatedNodeStillUsesItsOwnProcessAction(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(name string, args ...string) (string, error) { return "T\n", nil }
	runCombined = func(name string, args ...string) (string, error) {
		t.Fatal("Node must not inherit an engine command")
		return "", nil
	}
	var signalled int
	termProcess = func(pid int) error { signalled = pid; return nil }
	l := scan.Listener{PID: 42, StartTime: "T", Command: "node", CommandLine: "node docker-proxy.js --engine remote", Ports: []string{"3000"}}
	if _, err := Stop(l, intel.Make(l, intel.Env{})); err != nil || signalled != l.PID {
		t.Fatalf("own process action lost: PID=%d err=%v", signalled, err)
	}
}
