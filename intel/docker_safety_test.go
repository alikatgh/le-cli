package intel

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

func TestDetectNeverQueriesAmbientDockerEngine(t *testing.T) {
	original := detectOutput
	t.Cleanup(func() { detectOutput = original })
	var calls []string
	detectOutput = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name)
		if name != "brew" && name != "launchctl" {
			t.Fatalf("unexpected engine query: %s %v", name, args)
		}
		return nil, nil
	}
	for _, endpoint := range []string{"ssh://remote-engine", "tcp://remote-engine:2376", "unix:///tmp/forwarded-engine.sock", ""} {
		t.Setenv("DOCKER_HOST", endpoint)
		t.Setenv("DOCKER_CONTEXT", "production")
		env := Detect()
		if len(env.DockerByPort) != 0 {
			t.Fatalf("ambient engine publications collected for %q: %+v", endpoint, env.DockerByPort)
		}
	}
	if len(calls) != 8 {
		t.Fatalf("supported manager collectors changed unexpectedly: %v", calls)
	}
}

func TestPublicationCannotGrantContainerIdentityOrAction(t *testing.T) {
	for _, publication := range []string{
		strings.Repeat("a", 64) + "\tremote-web\t0.0.0.0:3000->3000/tcp",
		"short\tremote-web\t0.0.0.0:3000->3000/tcp",
		"one\tremote-web\t0.0.0.0:3000->3000/tcp\ntwo\tother\t0.0.0.0:3000->3000/tcp",
		"", // missing daemon/ownership data
	} {
		l := scan.Listener{PID: 42, Ports: []string{"3000"}, Command: "node", CommandLine: "node server.js", Cwd: "/work/docker-proxy"}
		p := Make(l, Env{DockerByPort: parseDockerPorts(publication)})
		if p.Source == SrcContainer || p.Identity == "remote-web" || p.StopKind != StopTerm || p.StopArg != "" || p.StopArgID != "" {
			t.Fatalf("unrelated Node inherited publication %q: %+v", publication, p)
		}
		// A context switch after capture cannot turn a publication into authority.
		t.Setenv("DOCKER_CONTEXT", "different-engine-after-capture")
		if got := Make(l, Env{DockerByPort: parseDockerPorts(publication)}); got.StopKind != StopTerm || got.StopArg != "" {
			t.Fatalf("ambient context changed process action: %+v", got)
		}
	}
}

func TestDockerHelperRecognitionUsesExecutableIdentity(t *testing.T) {
	for _, l := range []scan.Listener{
		{Command: "docker-proxy"},
		{Command: "com.docke"},
		{Command: "docker-pr"},
		{Command: "docker-pro"},
		{Command: "docker-prox"},
		{Command: "rootlessk"},
		{Command: "rootlessp"},
		{Command: "OrbStack"},
		{Command: "colima"},
		{Command: "Rancher Desktop"},
		{CommandLine: "/usr/bin/docker-proxy -host-port 3000"},
		{CommandLine: "/Applications/Docker.app/Contents/MacOS/com.docker.backend"},
		{CommandLine: `"C:\Program Files\Docker\Docker\resources\com.docker.backend.exe" -watchdog`},
		{CommandLine: "/usr/bin/vpnkit"},
		{CommandLine: "/usr/bin/rootlesskit"},
		{CommandLine: "/usr/bin/gvproxy"},
	} {
		if !IsDockerHelper(l) {
			t.Errorf("known helper unrecognized: %+v", l)
		}
		p := Make(l, Env{})
		if p.StopKind != StopAvoid || p.StopArg != "" || p.StopArgID != "" || p.Evidence.Owner.Status != Unavailable {
			t.Errorf("unverified helper is actionable or claims an owner: %+v", p)
		}
		encoded, err := json.Marshal(p)
		if err != nil || !strings.Contains(string(encoded), `"stopKind":"avoid"`) || !strings.Contains(string(encoded), "ownership are unverified") {
			t.Errorf("JSON missing truthful manual refusal: %s, %v", encoded, err)
		}
	}
	for _, l := range []scan.Listener{
		{Command: "node", CommandLine: "node docker-proxy.js"},
		{Command: "node", CommandLine: "/work/docker-proxy/node app.js", Cwd: "/work/com.docker.backend"},
		{CommandLine: "python3 script.py --helper vpnkit"},
		{CommandLine: "/usr/bin/docker-proxy-test"},
	} {
		if IsDockerHelper(l) {
			t.Errorf("ordinary process falsely became a helper: %+v", l)
		}
	}
}
