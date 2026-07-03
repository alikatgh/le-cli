package intel

import (
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

// Characterization tests for Make — the classification decision tree that is
// the whole point of the package (what a process is, how risky it is, and how
// to stop it). Each case pins one branch. Assertions cover only the fields
// that discriminate the branch, so unrelated copy edits (Explain/Note/Warning
// wording) don't churn the suite; the behavioral contract other packages rely
// on (Identity, Source, Kind, Risk, StopKind, StopArg) is what's locked down.

type want struct {
	identity string
	source   Source
	kind     Kind
	risk     Risk
	stop     StopKind
	stopArg  string // checked only when non-empty
}

func check(t *testing.T, name string, got Profile, w want) {
	t.Helper()
	if got.Identity != w.identity {
		t.Errorf("%s: Identity = %q, want %q", name, got.Identity, w.identity)
	}
	if got.Source != w.source {
		t.Errorf("%s: Source = %q, want %q", name, got.Source, w.source)
	}
	if got.Kind != w.kind {
		t.Errorf("%s: Kind = %q, want %q", name, got.Kind, w.kind)
	}
	if got.Risk != w.risk {
		t.Errorf("%s: Risk = %q, want %q", name, got.Risk, w.risk)
	}
	if got.StopKind != w.stop {
		t.Errorf("%s: StopKind = %q, want %q", name, got.StopKind, w.stop)
	}
	if w.stopArg != "" && got.StopArg != w.stopArg {
		t.Errorf("%s: StopArg = %q, want %q", name, got.StopArg, w.stopArg)
	}
}

func emptyEnv() Env {
	return Env{BrewStarted: map[string]bool{}, DockerByPort: map[string]dockerContainer{}}
}

func TestMakeDockerContainer(t *testing.T) {
	l := scan.Listener{PID: 100, Ports: []string{"3000"}, CommandLine: "/usr/bin/docker-proxy"}
	env := Env{
		BrewStarted:  map[string]bool{},
		DockerByPort: map[string]dockerContainer{"3000": {name: "web", id: "abc123"}},
	}
	got := Make(l, env)
	check(t, "docker", got, want{identity: "web", source: SrcContainer, kind: Other, risk: Med, stop: StopDocker, stopArg: "web"})
	if got.StopArgID != "abc123" {
		t.Errorf("docker: StopArgID = %q, want abc123", got.StopArgID)
	}
}

func TestMakeDatabases(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		w    want
	}{
		{"redis-brew", "/opt/homebrew/Cellar/redis/7.2.0/bin/redis-server", want{"Redis", SrcHomebrew, Database, High, StopBrew, "redis"}},
		{"mongodb-brew", "/opt/homebrew/Cellar/mongodb-community/7.0/bin/mongod", want{"MongoDB", SrcHomebrew, Database, High, StopBrew, "mongodb-community"}},
		{"postgres-brew", "/opt/homebrew/Cellar/postgresql@14/14.5/bin/postgres", want{"Postgres", SrcHomebrew, Database, High, StopBrew, "postgresql"}},
		{"mysql-brew", "/opt/homebrew/Cellar/mysql/8.0/bin/mysqld", want{"MySQL", SrcHomebrew, Database, High, StopBrew, "mysql"}},
		{"mariadb-brew", "/opt/homebrew/Cellar/mariadb/11.0/bin/mariadbd", want{"MariaDB", SrcHomebrew, Database, High, StopBrew, "mariadb"}},
	}
	for _, c := range cases {
		l := scan.Listener{PID: 1, Ports: []string{"5432"}, CommandLine: c.cmd}
		check(t, c.name, Make(l, emptyEnv()), c.w)
	}
}

func TestMakeRedisWithoutBrewUsesTerm(t *testing.T) {
	// A redis binary NOT under the Homebrew prefix has no formula, so there's
	// no `brew services stop` to recommend — it falls back to a careful TERM,
	// but stays High risk and identified.
	l := scan.Listener{PID: 1, Ports: []string{"6379"}, CommandLine: "/usr/local/bin/redis-server"}
	got := Make(l, emptyEnv())
	check(t, "redis-raw", got, want{identity: "Redis", source: SrcTerminal, kind: Database, risk: High, stop: StopTerm})
}

func TestMakeOllama(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"11434"}, CommandLine: "/usr/local/bin/ollama serve"}
	got := Make(l, emptyEnv())
	check(t, "ollama", got, want{identity: "Ollama", source: SrcTerminal, kind: AI, risk: Med, stop: StopTerm})
}

func TestMakeOllamaBrewManagedOwnedByHomebrew(t *testing.T) {
	// A brew-managed Ollama must report Source=homebrew (the Owner column),
	// consistent with its StopKind=brew — not "terminal".
	l := scan.Listener{PID: 1, Ports: []string{"11434"}, CommandLine: "/opt/homebrew/Cellar/ollama/0.1.0/bin/ollama serve"}
	env := Env{BrewStarted: map[string]bool{"ollama": true}, DockerByPort: map[string]dockerContainer{}}
	got := Make(l, env)
	check(t, "ollama-brew", got, want{identity: "Ollama", source: SrcHomebrew, kind: AI, risk: Med, stop: StopBrew, stopArg: "ollama"})
}

func TestMakeDevServers(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		id   string
	}{
		{"uvicorn", "/venv/bin/python -m uvicorn app:app", "FastAPI/Uvicorn"},
		{"django", "python /app/manage.py runserver", "Django dev server"},
		{"flask", "/venv/bin/flask run", "Flask dev server"},
		{"mkdocs", "/venv/bin/mkdocs serve", "MkDocs preview"},
		{"vite", "node /app/node_modules/.bin/vite", "Node web dev server"},
	}
	for _, c := range cases {
		l := scan.Listener{PID: 1, Ports: []string{"8000"}, CommandLine: c.cmd}
		got := Make(l, emptyEnv())
		if got.Identity != c.id {
			t.Errorf("%s: Identity = %q, want %q", c.name, got.Identity, c.id)
		}
		if got.Source != SrcFramework {
			t.Errorf("%s: Source = %q, want framework", c.name, got.Source)
		}
		if got.Risk != Low {
			t.Errorf("%s: Risk = %q, want low", c.name, got.Risk)
		}
		if got.StopKind != StopTerm {
			t.Errorf("%s: StopKind = %q, want term", c.name, got.StopKind)
		}
	}
}

func TestMakeInterpreterModule(t *testing.T) {
	// `python3 -m http.server 8000` should read as the module it's running,
	// not a generic "Python".
	l := scan.Listener{PID: 1, Ports: []string{"8000"}, CommandLine: "python3 -m http.server 8000"}
	got := Make(l, emptyEnv())
	check(t, "http.server", got, want{identity: "http.server", source: SrcTerminal, kind: Python, risk: Low, stop: StopTerm})
}

func TestMakeInterpreterScript(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"8000"}, CommandLine: "node /Users/me/server.mjs"}
	got := Make(l, emptyEnv())
	check(t, "script", got, want{identity: "server.mjs", source: SrcTerminal, kind: Node, risk: Low, stop: StopTerm})
}

func TestMakeNodeServiceFallback(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"3000"}, CommandLine: "npm run start"}
	got := Make(l, emptyEnv())
	check(t, "node-service", got, want{identity: "Node service", source: SrcFramework, kind: Node, risk: Low, stop: StopTerm})
}

func TestMakePythonServiceFallback(t *testing.T) {
	// `python -c ...` bails out of interpreterIdentity (no module/script), so
	// it lands on the generic Python-service fallback.
	l := scan.Listener{PID: 1, Ports: []string{"5000"}, CommandLine: "python -c code"}
	got := Make(l, emptyEnv())
	check(t, "python-service", got, want{identity: "Python service", source: SrcFramework, kind: Python, risk: Low, stop: StopTerm})
}

func TestMakeEditorLanguageServer(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"7000"}, CommandLine: "/opt/sourcekit/sourcekit-language-server"}
	got := Make(l, emptyEnv())
	check(t, "editor", got, want{identity: "Editor language service", source: SrcIDE, kind: Editor, risk: Med, stop: StopAvoid})
}

func TestMakeHomebrewFallback(t *testing.T) {
	// A Homebrew-managed binary that isn't one of the named databases/services
	// still gets a `brew services stop` recommendation keyed on its formula.
	l := scan.Listener{PID: 1, Ports: []string{"9000"}, CommandLine: "/opt/homebrew/Cellar/some-tool/1.0/bin/some-tool"}
	got := Make(l, emptyEnv())
	check(t, "brew-fallback", got, want{identity: "some-tool", source: SrcHomebrew, kind: Other, risk: Med, stop: StopBrew, stopArg: "some-tool"})
}

func TestMakeSystemService(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"5000"}, CommandLine: "/usr/libexec/some-daemon"}
	got := Make(l, emptyEnv())
	check(t, "system", got, want{identity: "macOS service", source: SrcMacOS, kind: System, risk: High, stop: StopAvoid})
}

func TestMakeBackgroundApp(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"51000"}, CommandLine: "/Applications/Rewind.app/Contents/MacOS/RewindHelper"}
	got := Make(l, emptyEnv())
	check(t, "bg-app", got, want{identity: "App helper", source: SrcApp, kind: App, risk: High, stop: StopAvoid})
}

func TestMakeWildcardOpenListener(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"8080"}, Addrs: []string{"*:8080"}, CommandLine: "/Users/me/myserver"}
	got := Make(l, emptyEnv())
	check(t, "wildcard", got, want{identity: "Open local listener", source: SrcTerminal, kind: Other, risk: Med, stop: StopTerm})
}

func TestMakeUnknownFallback(t *testing.T) {
	l := scan.Listener{PID: 42, Ports: []string{"9999"}, Addrs: []string{"127.0.0.1:9999"}, Command: "mysteryapp", CommandLine: "/Users/me/mysteryapp"}
	got := Make(l, emptyEnv())
	check(t, "unknown", got, want{identity: "mysteryapp", source: SrcTerminal, kind: Other, risk: Low, stop: StopTerm})
}

// Docker attribution wins over everything else — even a process whose command
// line would otherwise classify as a database — because the port maps to a
// container, and stopping the container is the right action.
func TestMakeDockerBeatsCommandLine(t *testing.T) {
	l := scan.Listener{PID: 1, Ports: []string{"6379"}, CommandLine: "redis-server"}
	env := Env{
		BrewStarted:  map[string]bool{},
		DockerByPort: map[string]dockerContainer{"6379": {name: "cache", id: "d00d"}},
	}
	got := Make(l, env)
	if got.Source != SrcContainer || got.StopKind != StopDocker || got.StopArg != "cache" {
		t.Errorf("docker-vs-db: got source=%q stop=%q arg=%q, want container/docker/cache", got.Source, got.StopKind, got.StopArg)
	}
}

func TestMakeLaunchdAgentRoutedThroughSupervisor(t *testing.T) {
	// The commenter's test case: a KeepAlive user agent running a generic
	// command. TERM would just make launchd respawn it — the stop must go
	// through the supervisor, and the owner column must say so.
	l := scan.Listener{PID: 4242, Ports: []string{"9997"}, CommandLine: "/usr/bin/python3 -m http.server 9997"}
	env := Env{
		BrewStarted:  map[string]bool{},
		DockerByPort: map[string]dockerContainer{},
		LaunchdByPID: map[int]string{4242: "com.example.devserver"},
	}
	got := Make(l, env)
	if got.Source != SrcLaunchd {
		t.Errorf("launchd agent: Source = %q, want %q", got.Source, SrcLaunchd)
	}
	if got.StopKind != StopLaunchd || got.StopArg != "com.example.devserver" {
		t.Errorf("launchd agent: StopKind/Arg = %q/%q, want launchd/com.example.devserver", got.StopKind, got.StopArg)
	}
	if got.Risk != Med {
		t.Errorf("launchd agent: Risk = %q, want %q (supervised implies depended-on)", got.Risk, Med)
	}
	if want := "launchctl bootout " + LaunchdDomainTarget("com.example.devserver"); got.StopLabel != want {
		t.Errorf("launchd agent: StopLabel = %q, want %q", got.StopLabel, want)
	}
}

func TestMakeBrewWinsOverLaunchdLabel(t *testing.T) {
	// Brew services ARE launchd jobs, so a brew-managed listener will also
	// appear in launchctl list. The brew route must keep precedence — it is
	// the correct front-end for that job.
	l := scan.Listener{PID: 77, Ports: []string{"6379"}, CommandLine: "/opt/homebrew/Cellar/redis/7.2.0/bin/redis-server"}
	env := Env{
		BrewStarted:  map[string]bool{"redis": true},
		DockerByPort: map[string]dockerContainer{},
		LaunchdByPID: map[int]string{77: "homebrew.mxcl.redis"},
	}
	got := Make(l, env)
	check(t, "brew-over-launchd", got, want{identity: "Redis", source: SrcHomebrew, kind: Database, risk: High, stop: StopBrew, stopArg: "redis"})
}

func TestMakeAvoidStaysAvoidDespiteLaunchdLabel(t *testing.T) {
	// System daemons are launchd-managed by definition; that must not turn
	// a refused row into an auto-stoppable one.
	l := scan.Listener{PID: 88, Ports: []string{"5000"}, CommandLine: "/usr/libexec/somethingd"}
	env := Env{
		BrewStarted:  map[string]bool{},
		DockerByPort: map[string]dockerContainer{},
		LaunchdByPID: map[int]string{88: "com.apple.somethingd"},
	}
	got := Make(l, env)
	if got.StopKind != StopAvoid {
		t.Errorf("system daemon with launchd label: StopKind = %q, must stay %q", got.StopKind, StopAvoid)
	}
}

func TestMakeAvoidRowNamesItsLaunchdLabel(t *testing.T) {
	// "Does it surface the supervisor so you know to disable that first?" —
	// refused rows must still NAME the launchd label, even though the stop
	// stays refused.
	l := scan.Listener{PID: 88, Ports: []string{"5000"}, CommandLine: "/usr/libexec/somethingd"}
	env := Env{
		BrewStarted:  map[string]bool{},
		DockerByPort: map[string]dockerContainer{},
		LaunchdByPID: map[int]string{88: "com.apple.somethingd"},
	}
	got := Make(l, env)
	if got.StopKind != StopAvoid {
		t.Fatalf("StopKind = %q, must stay %q", got.StopKind, StopAvoid)
	}
	if !strings.Contains(got.Note, "com.apple.somethingd") {
		t.Errorf("Note = %q, must name the launchd label", got.Note)
	}
}
