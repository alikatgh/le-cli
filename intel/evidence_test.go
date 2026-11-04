package intel

import (
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

func TestEvidenceDistinguishesObservationAndInference(t *testing.T) {
	l := scan.Listener{PID: 42, Command: "node", CommandLine: "node server.js", Cwd: "/work/site", StartTime: "started", Ports: []string{"3000"}, Addrs: []string{"*:3000"}}
	p := Make(l, Env{})
	for name, item := range map[string]EvidenceItem{"listener": p.Evidence.Listener, "command": p.Evidence.Command, "directory": p.Evidence.Directory, "start": p.Evidence.StartTime} {
		if item.Status != Observed || item.Detail == "" {
			t.Errorf("%s evidence = %+v", name, item)
		}
	}
	if p.Evidence.Classification.Status != Inferred || p.Evidence.Owner.Status != Inferred {
		t.Fatalf("heuristics must stay inferred: %+v", p.Evidence)
	}
	// Missing ps values and legacy zero CPU/RSS do not establish observations.
	missing := Make(scan.Listener{PID: 42, Command: "node", CommandLine: "node"}, Env{}).Evidence
	if missing.Command.Status != Unavailable || missing.Directory.Status != Unavailable || missing.StartTime.Status != Unavailable || missing.Listener.Status != Unavailable {
		t.Fatalf("unavailable fields claimed as observed: %+v", missing)
	}
}

func TestEvidenceNameFallbackIsNotCommandObservation(t *testing.T) {
	for _, name := range []string{"node.exe", "pid 42", "node"} {
		l := scan.Listener{PID: 42, Command: name, CommandLine: name}
		if HasCommandLine(l) || Make(l, Env{}).Evidence.Command.Status != Unavailable {
			t.Errorf("name-only fallback %q claimed full argv", name)
		}
	}
}

func TestEvidenceOwnerUsesExactObservation(t *testing.T) {
	l := scan.Listener{PID: 42, Command: "docker-proxy", CommandLine: "/usr/bin/docker-proxy -host-port 3000", Ports: []string{"3000"}}
	e := Make(l, Env{DockerByPort: map[string]dockerContainer{"3000": {name: "web", id: "abc"}}, LaunchdByPID: map[int]string{42: "app.helper"}}).Evidence
	if e.Owner.Status != Inferred || !strings.Contains(e.Owner.Detail, "process ownership unverified") {
		t.Fatalf("port association claimed as process ownership: %+v", e.Owner)
	}
	l.Command, l.CommandLine = "node", "node server.js"
	e = Make(l, Env{LaunchdByPID: map[int]string{42: "dev.server", 43: "other.server"}}).Evidence
	if e.Owner.Status != Observed || !strings.Contains(e.Owner.Detail, "dev.server") || strings.Contains(e.Owner.Detail, "other.server") {
		t.Fatalf("launchd PID observation lost: %+v", e.Owner)
	}
}

func TestDockerPublicationOnlyAttributesTCP(t *testing.T) {
	for _, ports := range []string{"0.0.0.0:3000->3000/udp", "0.0.0.0:8000-8010->8000-8010/udp", "[::]:3000->3000/udp"} {
		if got := parseDockerPorts("abc\tudp-only\t" + ports); len(got) != 0 {
			t.Fatalf("UDP publication attributed TCP listener: %v", got)
		}
	}
	got := parseDockerPorts("udp\tdns\t0.0.0.0:3000->53/udp\ntcp\tweb\t0.0.0.0:3000->3000/tcp, [::]:8000-8002->8000-8002/tcp, 127.0.0.1:9000->9000/udp")
	if len(got) != 4 || got["3000"].name != "web" || got["8002"].id != "tcp" {
		t.Fatalf("mixed TCP/UDP publication map = %v", got)
	}
}

func TestEvidenceExecutableFallbackIsNotObservedArgv(t *testing.T) {
	l := scan.Listener{PID: 42, Command: "node.exe", CommandLine: `C:\node\node.exe`, CommandLineFallback: true}
	if HasCommandLine(l) || Make(l, Env{}).Evidence.Command.Status != Unavailable {
		t.Fatal("executable fallback claimed captured argv")
	}
}
