package intel

import (
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

func TestPrettyModule(t *testing.T) {
	cases := map[string]string{
		"http.server":      "http.server",
		"SimpleHTTPServer": "http.server",
		"uvicorn":          "Uvicorn",
		"gunicorn":         "Gunicorn",
		"flask":            "Flask",
		"django":           "Django",
		"streamlit":        "Streamlit",
		"jupyter":          "Jupyter",
		"fastapi":          "FastAPI",
		"my_custom_mod":    "my_custom_mod", // default: passthrough
	}
	for in, wantOut := range cases {
		if got := prettyModule(in); got != wantOut {
			t.Errorf("prettyModule(%q) = %q, want %q", in, got, wantOut)
		}
	}
}

func TestModuleExplain(t *testing.T) {
	cases := []struct {
		mod, interp, want string
	}{
		{"http.server", "python3", "Python's built-in static file server, serving the working folder over HTTP."},
		{"SimpleHTTPServer", "python3", "Python's built-in static file server, serving the working folder over HTTP."},
		{"uvicorn", "python3", "ASGI server commonly running FastAPI or Starlette apps."},
		{"gunicorn", "python3", "Production WSGI HTTP server for Python web apps."},
		{"streamlit", "python3", "Streamlit dashboard running locally."},
		{"jupyter", "python3", "Jupyter notebook or lab server."},
		{"foo", "python3", "python3 module `foo` running locally."}, // default
	}
	for _, c := range cases {
		if got := moduleExplain(c.mod, c.interp); got != c.want {
			t.Errorf("moduleExplain(%q, %q) = %q, want %q", c.mod, c.interp, got, c.want)
		}
	}
}

func TestCanonicalInterp(t *testing.T) {
	cases := map[string]string{
		"python3.11":      "python3",
		"/usr/bin/python": "python3",
		"py":              "python3",
		"ruby2.7":         "ruby",
		"/usr/bin/ruby":   "ruby",
		"node":            "node",
		"deno":            "deno",
		"bun":             "bun",
	}
	for in, wantOut := range cases {
		if got := canonicalInterp(in); got != wantOut {
			t.Errorf("canonicalInterp(%q) = %q, want %q", in, got, wantOut)
		}
	}
}

func TestInterpreterIdentityEdgeCases(t *testing.T) {
	if _, ok := interpreterIdentity(""); ok {
		t.Error("empty command should not resolve an interpreter identity")
	}
	if _, ok := interpreterIdentity("python -c 'print(1)'"); ok {
		t.Error("python -c should bail (no module/script to name)")
	}
	if _, ok := interpreterIdentity("python3"); ok {
		t.Error("bare interpreter with no args should not resolve")
	}
	if _, ok := interpreterIdentity("/usr/bin/some-daemon --serve"); ok {
		t.Error("non-interpreter command should not resolve")
	}
	// A ruby script resolves with Other kind (not Python/Node).
	id, ok := interpreterIdentity("ruby /app/server.rb")
	if !ok || id.title != "server.rb" || id.kind != Other {
		t.Errorf("ruby script: got ok=%v title=%q kind=%q, want true/server.rb/other", ok, id.title, id.kind)
	}
	// `-m gunicorn` resolves to the prettified module, Python kind.
	id, ok = interpreterIdentity("python3 -m gunicorn app:app")
	if !ok || id.title != "Gunicorn" || id.kind != Python {
		t.Errorf("gunicorn: got ok=%v title=%q kind=%q, want true/Gunicorn/python", ok, id.title, id.kind)
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName(scan.Listener{Command: "node"}); got != "node" {
		t.Errorf("Command set: got %q, want node", got)
	}
	if got := displayName(scan.Listener{CommandLine: "/usr/bin/python3 app.py"}); got != "python3" {
		t.Errorf("CommandLine only: got %q, want python3", got)
	}
	if got := displayName(scan.Listener{PID: 42}); got != "pid 42" {
		t.Errorf("neither: got %q, want 'pid 42'", got)
	}
}

func TestIsSystem(t *testing.T) {
	cases := []struct {
		name string
		l    scan.Listener
		want bool
	}{
		{"system prefix", scan.Listener{CommandLine: "/System/Library/CoreServices/foo"}, true},
		{"libexec prefix", scan.Listener{CommandLine: "/usr/libexec/rapportd"}, true},
		{"usr-sbin prefix", scan.Listener{CommandLine: "/usr/sbin/foo"}, true},
		{"sbin prefix", scan.Listener{CommandLine: "/sbin/foo"}, true},
		{"root-owned rapportd", scan.Listener{User: "root", CommandLine: "/opt/rapportd"}, true},
		{"root-owned controlcenter", scan.Listener{User: "root", CommandLine: "/x/controlcenter"}, true},
		{"root but homebrew cellar excluded", scan.Listener{User: "root", CommandLine: "/opt/homebrew/Cellar/redis/x/rapportd"}, false},
		{"root but in /users excluded", scan.Listener{User: "root", CommandLine: "/Users/me/rapportd"}, false},
		{"non-root rapportd", scan.Listener{User: "alice", CommandLine: "rapportd"}, false},
		{"ordinary user app", scan.Listener{User: "alice", CommandLine: "/Users/me/app"}, false},
	}
	for _, c := range cases {
		if got := isSystem(c.l); got != c.want {
			t.Errorf("isSystem(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSourceFor(t *testing.T) {
	// sourceFor is the Source safety net Make falls back to when a branch
	// leaves Source unset. Priority order: container > brew > system > editor
	// kind > bg app > has-cwd/cmdline > unknown.
	cases := []struct {
		name                        string
		l                           scan.Listener
		p                           Profile
		container, brew, system, bg bool
		want                        Source
	}{
		{"container wins", scan.Listener{}, Profile{}, true, true, true, true, SrcContainer},
		{"brew next", scan.Listener{}, Profile{}, false, true, true, true, SrcHomebrew},
		{"system next", scan.Listener{}, Profile{}, false, false, true, true, SrcMacOS},
		{"editor kind", scan.Listener{}, Profile{Kind: Editor}, false, false, false, false, SrcIDE},
		{"bg app", scan.Listener{}, Profile{}, false, false, false, true, SrcApp},
		{"has cmdline -> terminal", scan.Listener{CommandLine: "x"}, Profile{}, false, false, false, false, SrcTerminal},
		{"has cwd -> terminal", scan.Listener{Cwd: "/x"}, Profile{}, false, false, false, false, SrcTerminal},
		{"nothing -> unknown", scan.Listener{}, Profile{}, false, false, false, false, SrcUnknown},
	}
	for _, c := range cases {
		if got := sourceFor(c.l, c.p, c.container, c.brew, c.system, c.bg); got != c.want {
			t.Errorf("sourceFor(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}
