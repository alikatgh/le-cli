package intel

import (
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

func TestKubectlForwardIdentity(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{"svc with mapping", "kubectl port-forward svc/frontend 8080:80", "K8s forward: svc/frontend 8080→80"},
		{"pod bare port", "kubectl port-forward pod/db 5432", "K8s forward: pod/db 5432"},
		{"namespace flag", "kubectl port-forward svc/api 3000:3000 -n prod", "K8s forward: svc/api 3000→3000 (prod)"},
		{"namespace equals", "kubectl port-forward --namespace=staging deploy/web 8080:80", "K8s forward: deploy/web 8080→80 (staging)"},
		{"global flags before subcommand", "kubectl --context minikube port-forward svc/x 9000:9000", "K8s forward: svc/x 9000→9000"},
		{"unparseable falls back", "kubectl port-forward", "K8s port-forward"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := kubectlForwardIdentity(c.cmd)
			if got != c.want {
				t.Errorf("kubectlForwardIdentity(%q) = %q, want %q", c.cmd, got, c.want)
			}
		})
	}
}

func TestCloudflaredIdentity(t *testing.T) {
	cases := []struct{ cmd, want string }{
		{"cloudflared tunnel run my-tunnel", "Cloudflare Tunnel: my-tunnel"},
		{"cloudflared tunnel --url http://localhost:3000", "Cloudflare quick tunnel → localhost:3000"},
		{"cloudflared tunnel --url=https://localhost:8443", "Cloudflare quick tunnel → localhost:8443"},
		{"cloudflared", "Cloudflare Tunnel"},
	}
	for _, c := range cases {
		if got := cloudflaredIdentity(c.cmd); got != c.want {
			t.Errorf("cloudflaredIdentity(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestMakeRecognizesKubectlForward(t *testing.T) {
	l := scan.Listener{
		PID: 4242, Ports: []string{"8080"},
		Command:     "kubectl",
		CommandLine: "kubectl port-forward svc/frontend 8080:80 -n prod",
	}
	p := Make(l, Env{})
	if p.Kind != Tunnel {
		t.Errorf("Kind = %q, want tunnel", p.Kind)
	}
	if !strings.Contains(p.Identity, "svc/frontend") {
		t.Errorf("Identity = %q, want it to name the forward target", p.Identity)
	}
	if p.StopKind != StopTerm {
		t.Errorf("StopKind = %q, want term (kubectl exits cleanly on TERM)", p.StopKind)
	}
}

func TestMakeRecognizesCloudflared(t *testing.T) {
	l := scan.Listener{
		PID: 4243, Ports: []string{"39555"},
		Command:     "cloudflared",
		CommandLine: "cloudflared tunnel run prod-tunnel",
	}
	p := Make(l, Env{})
	if p.Kind != Tunnel {
		t.Errorf("Kind = %q, want tunnel", p.Kind)
	}
	if p.Identity != "Cloudflare Tunnel: prod-tunnel" {
		t.Errorf("Identity = %q", p.Identity)
	}
}

func TestIsPortMapping(t *testing.T) {
	for s, want := range map[string]bool{
		"8080": true, "8080:80": true, ":80": true,
		"svc/foo": false, "8080:80:1": false, "": false, "-n": false,
	} {
		if got := isPortMapping(s); got != want {
			t.Errorf("isPortMapping(%q) = %v, want %v", s, got, want)
		}
	}
}
