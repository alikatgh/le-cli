// Package intel ports the macOS app's ProcessProfile logic: given a listener,
// it identifies what the process is, how risky it is to stop, and the right
// way to stop it (TERM vs `brew services stop` vs `docker stop`). The decision
// tree, the word-boundary matcher, and the interpreter resolver mirror
// Sources/LocalhostExplorer/Models/ProcessIntelligence.swift.
package intel

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/alikatgh/le-cli/scan"
)

type Kind string
type Source string
type Risk string
type StopKind string

const (
	Python   Kind = "python"
	Node     Kind = "node"
	Database Kind = "database"
	AI       Kind = "ai"
	Editor   Kind = "editor"
	System   Kind = "macos"
	App      Kind = "app"
	Other    Kind = "other"
)

const (
	SrcTerminal  Source = "terminal"
	SrcHomebrew  Source = "homebrew"
	SrcContainer Source = "container"
	SrcFramework Source = "framework"
	SrcIDE       Source = "ide"
	SrcLaunchd   Source = "launchd"
	SrcMacOS     Source = "macos"
	SrcApp       Source = "app"
	SrcUnknown   Source = "unknown"
)

const (
	Low  Risk = "low"
	Med  Risk = "medium"
	High Risk = "high"
)

// StopKind selects how kill.Stop executes the recommended action.
const (
	StopTerm    StopKind = "term"    // SIGTERM to the PID
	StopBrew    StopKind = "brew"    // brew services stop <arg>
	StopDocker  StopKind = "docker"  // docker stop <arg>
	StopLaunchd StopKind = "launchd" // launchctl bootout gui/<uid>/<arg>
	StopAvoid   StopKind = "avoid"   // no safe automatic action
)

// Profile is everything the UI needs to render and act on a listener. Tagged
// to match scan.Listener's lowerCamelCase `le list --json` convention — a
// script written against `.profile.stopArgID` shouldn't have to special-case
// this one nested object as PascalCase.
type Profile struct {
	Identity   string   `json:"identity"`
	Kind       Kind     `json:"kind"`
	Source     Source   `json:"source"`
	Confidence int      `json:"confidence"`
	Risk       Risk     `json:"risk"`
	StopKind   StopKind `json:"stopKind"`
	StopArg    string   `json:"stopArg"`   // brew formula or container name
	StopArgID  string   `json:"stopArgID"` // container short ID, re-verified before stopping (StopDocker only)
	StopLabel  string   `json:"stopLabel"` // human description of the stop action
	Restart    string   `json:"restart"`
	Note       string   `json:"note"`
	Warning    string   `json:"warning"`
	Explain    string   `json:"explain"`
}

// dockerContainer identifies a container by both the name used in
// `docker stop <name>` and its short ID, so kill.Stop can re-verify the name
// still points at the same container immediately before acting on it.
type dockerContainer struct {
	name string
	id   string
}

// Env is the environment-wide context gathered once per scan.
type Env struct {
	BrewStarted  map[string]bool            // formula -> started via `brew services`
	DockerByPort map[string]dockerContainer // port -> container identity
	LaunchdByPID map[int]string             // pid -> user-domain launchd label
}

// Detect gathers brew + docker + launchd context once. All are best-effort:
// missing tools just mean those branches stay empty.
func Detect() Env {
	return Env{
		BrewStarted:  brewStarted(),
		DockerByPort: dockerByPort(),
		LaunchdByPID: launchdByPID(),
	}
}

func brewStarted() map[string]bool {
	out, err := exec.Command("brew", "services", "list").Output()
	if err != nil {
		return map[string]bool{}
	}
	return parseBrewStarted(string(out))
}

// parseBrewStarted extracts the set of formulae `brew services` reports as
// active (started or scheduled), split out from the exec so it's testable.
// The name→managed mapping it produces gates Make's managedBrew classification.
func parseBrewStarted(out string) map[string]bool {
	m := map[string]bool{}
	for i, line := range strings.Split(out, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header / blank
		}
		f := strings.Fields(line)
		if len(f) >= 2 && (f[1] == "started" || f[1] == "scheduled") {
			m[f[0]] = true
		}
	}
	return m
}

// launchdByPID maps running PIDs to their user-domain launchd labels via
// `launchctl list` (no sudo; system-domain daemons are invisible here, which
// is fine — those already classify as macOS services and stay StopAvoid).
// Why this exists: a KeepAlive agent respawns the moment its PID dies, so a
// plain TERM turns into a ghost-chase — the supervisor is the thing to stop.
func launchdByPID() map[int]string {
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return map[int]string{} // not macOS, or launchctl unavailable
	}
	return parseLaunchdList(string(out))
}

// parseLaunchdList extracts pid -> label from `launchctl list` output
// (columns: PID, Status, Label; PID is "-" for loaded-but-not-running jobs).
// Tab-split first — labels are the last column and could in principle carry
// odd characters — with a whitespace-split fallback for safety.
func parseLaunchdList(out string) map[int]string {
	m := map[int]string{}
	for i, line := range strings.Split(out, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header / blank
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			f = strings.Fields(line)
		}
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(f[0]))
		if err != nil || pid <= 0 {
			continue // "-" (not running) or malformed
		}
		label := strings.TrimSpace(strings.Join(f[2:], "\t"))
		if label != "" {
			m[pid] = label
		}
	}
	return m
}

// LaunchdDomainTarget renders the launchctl domain target for a user-domain
// label — the exact argument `launchctl bootout` needs.
func LaunchdDomainTarget(label string) string {
	return "gui/" + strconv.Itoa(os.Getuid()) + "/" + label
}

// LaunchdLabelPID returns the PID currently running under the given
// user-domain label. kill.Stop calls this immediately before a bootout to
// confirm the label still maps to the process we scanned — labels, unlike
// PIDs, can be bootout'd and re-bootstrapped onto a different program
// between scan and stop.
//
// A package var, not a plain func, so kill's tests can stub the re-verify
// result without real launchd jobs.
var LaunchdLabelPID = func(label string) (int, bool) {
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return 0, false
	}
	for pid, l := range parseLaunchdList(string(out)) {
		if l == label {
			return pid, true
		}
	}
	return 0, false
}

var dockerPortRe = regexp.MustCompile(`(?:0\.0\.0\.0|127\.0\.0\.1|\[::\]|::):(\d+)->`)

func dockerByPort() map[string]dockerContainer {
	out, err := exec.Command("docker", "ps", "--format", "{{.ID}}\t{{.Names}}\t{{.Ports}}").Output()
	if err != nil {
		return map[string]dockerContainer{}
	}
	return parseDockerPorts(string(out))
}

// parseDockerPorts is split out from dockerByPort so it's testable without
// shelling out. A port that two different containers both claim — a real,
// if transient, state during a container restart or handoff — is dropped
// rather than silently attributed to whichever line happened to come last:
// a wrong attribution here would make kill.Stop issue `docker stop` against
// the wrong container.
func parseDockerPorts(out string) map[string]dockerContainer {
	m := map[string]dockerContainer{}
	ambiguous := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		id, name, ports := parts[0], parts[1], parts[2]
		for _, match := range dockerPortRe.FindAllStringSubmatch(ports, -1) {
			port := match[1]
			if existing, ok := m[port]; ok && existing.name != name {
				ambiguous[port] = true
				continue
			}
			m[port] = dockerContainer{name: name, id: id}
		}
	}
	for port := range ambiguous {
		delete(m, port)
	}
	return m
}

// DockerContainerID returns the short ID of the currently-running container
// with the given exact name, if exactly one matches. kill.Stop calls this
// immediately before a `docker stop` to confirm the name still points at the
// same container it did at scan time — container names, unlike PIDs, can be
// freed and reassigned to a completely different container.
//
// A package var, not a plain func, so kill's tests can stub the re-verify
// result without a running Docker daemon.
var DockerContainerID = func(name string) (string, bool) {
	// Docker's --filter name= value is regex-matched, not literal — a name
	// containing a regex metacharacter (".", "+", "*"... all legal in Docker
	// container names, and routine in docker-compose-generated names) would
	// otherwise match a differently-named container too. Escape it.
	//
	// #nosec G204 -- name is passed as a discrete exec argument (no shell), so
	// there's no command injection; regexp.QuoteMeta additionally neutralizes
	// the regex metacharacters that are the only injection surface here.
	out, err := exec.Command("docker", "ps", "--filter", "name=^"+regexp.QuoteMeta(name)+"$", "--format", "{{.ID}}").Output()
	if err != nil {
		return "", false
	}
	id := strings.TrimSpace(string(out))
	if id == "" || strings.Contains(id, "\n") {
		return "", false // none, or ambiguous — don't guess
	}
	return id, true
}

// BrewServiceKnown reports whether brew currently lists the given formula at
// all (started, stopped, error — any state). kill.Stop calls this immediately
// before `brew services stop`, in the same spirit as DockerContainerID's
// re-verification, though the underlying risk differs: a brew formula name
// isn't reassignable to a different service the way a Docker container name
// is (there's no "a different thing now answers to this label" scenario for
// brew), so this only catches the narrower case of the formula having been
// removed between scan and stop.
//
// This shells out to `brew services list`, which lists every known service
// and is measurably slower than a single-formula lookup. `brew services info
// <formula>` looks like the obvious faster replacement, but its exit code is
// identical (1) for "no such formula" and "formula exists but its tap is
// untrusted" — switching would silently misreport a legitimate untrusted-tap
// service as unknown and refuse to stop it. Kept as `list` on purpose; the
// latency is dominated by brew's process-startup overhead either way, so the
// single-lookup form wouldn't meaningfully help.
//
// A package var, not a plain func, so kill's tests can stub the check.
var BrewServiceKnown = func(formula string) bool {
	out, err := exec.Command("brew", "services", "list").Output()
	if err != nil {
		return true // brew itself unusable — don't block the stop attempt on this check
	}
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // header / blank
		}
		if strings.Fields(line)[0] == formula {
			return true
		}
	}
	return false
}

// Make is the port of ProcessProfile.make + ProcessSource.make.
func Make(l scan.Listener, env Env) Profile {
	text := strings.ToLower(strings.Join([]string{l.Command, l.CommandLine, l.Cwd}, " "))
	// Database identity comes from the BINARY, not the project folder: a listener
	// living in ~/postgres-migration is not Postgres. Match DB names against the
	// command only. Word boundaries alone don't help — "postgres" is a whole word
	// in "postgres-migration". Mirrors the mac app's commandText. (LE-314/316/328)
	commandText := strings.ToLower(l.Command + " " + l.CommandLine)
	kind := classifyKind(l)
	formula := brewFormula(l.CommandLine)
	managedBrew := formula != "" && env.BrewStarted[formula]
	container := containerFor(l, env)
	wildcard := hasWildcard(l.Addrs)
	system := isSystem(l)
	bgApp := isBackgroundApp(l)

	word := func(n string) bool { return wordMatch(text, n) }
	p := Profile{Kind: kind, Confidence: 45, Risk: Low, StopKind: StopTerm}
	p.StopLabel = "Send TERM to PID " + itoa(l.PID)

	switch {
	case container.name != "":
		p.Identity, p.Source, p.Confidence, p.Risk = container.name, SrcContainer, 88, Med
		p.StopKind, p.StopArg, p.StopArgID, p.StopLabel = StopDocker, container.name, container.id, "docker stop "+container.name
		p.Restart = "docker start " + container.name
		p.Explain = "Published by a Docker-compatible container."
		p.Warning = "Stopping the container is safer than killing the Docker helper process."

	case strings.Contains(commandText, "redis-server") || formula == "redis":
		database(&p, "Redis", pick(formula == "redis", 96, 90), "In-memory database/cache used by local apps, queues, and sessions.", l, formula, managedBrew)

	case wordMatch(commandText, "mongod") || strings.Contains(formula, "mongodb"):
		database(&p, "MongoDB", pick(formula != "", 95, 88), "Document database local projects may depend on for app data.", l, formula, managedBrew)

	case strings.Contains(commandText, "postgres") || strings.Contains(commandText, "postmaster") || formula == "postgresql":
		database(&p, "Postgres", pick(formula != "", 94, 86), "Relational database. Stopping it drops local connections.", l, formula, managedBrew)

	case strings.Contains(commandText, "mysql") || strings.Contains(commandText, "mariadb") || formula == "mysql" || formula == "mariadb":
		name := "MySQL"
		if strings.Contains(commandText, "mariadb") || formula == "mariadb" {
			name = "MariaDB"
		}
		database(&p, name, pick(formula != "", 94, 86), name+" relational database. Local projects may lose connections.", l, formula, managedBrew)

	case word("ollama"):
		p.Identity, p.Kind, p.Confidence, p.Risk = "Ollama", AI, pick(formula == "ollama", 96, 91), Med
		p.Source = SrcTerminal
		p.Explain = "Serves local AI models over HTTP, usually on port 11434."
		p.Note = "Local AI service: apps using local models may fail when it stops."
		p.Warning = "Stopping Ollama can interrupt local AI features in other apps."
		if formula != "" {
			// Brew-managed: own it as such (Source drives the Owner column), the
			// same as database() and the generic brew branch — otherwise a
			// launchd-managed Ollama misreports as a plain terminal process.
			p.Source = SrcHomebrew
			p.StopKind, p.StopArg, p.StopLabel = StopBrew, formula, "brew services stop "+formula
			p.Restart = "brew services start " + formula
		} else {
			p.Restart = "ollama serve"
		}

	case strings.Contains(text, "uvicorn") || strings.Contains(text, "fastapi"):
		devServer(&p, "FastAPI/Uvicorn", 92, "Python API dev server for a local FastAPI app.", "Restart the uvicorn/FastAPI command from the project terminal.", l)

	case word("django") || strings.Contains(text, "manage.py runserver"):
		devServer(&p, "Django dev server", 90, "Python web server launched by Django for local development.", "Run python manage.py runserver again.", l)

	case word("flask"):
		devServer(&p, "Flask dev server", 88, "Python web server launched by Flask for local development.", "Run flask run again.", l)

	case strings.Contains(text, "mkdocs"):
		devServer(&p, "MkDocs preview", 88, "Local documentation preview server.", "Run mkdocs serve again.", l)

	case word("vite") || word("next") || word("nuxt") || word("astro"):
		devServer(&p, "Node web dev server", 86, "JavaScript framework running a local dev server.", "Run npm/pnpm/yarn dev again.", l)

	default:
		if id, ok := interpreterIdentity(l.CommandLine); ok {
			p.Identity, p.Kind, p.Confidence = id.title, id.kind, id.confidence
			p.Source = SrcTerminal
			p.Explain = id.explanation
			p.Restart = "Run `" + id.restart + "` again."
			p.Note = "Process you launched from a terminal."
		} else if word("node") || word("npm") || word("pnpm") || word("yarn") || word("bun") {
			devServer(&p, "Node service", 78, "Node-based local service or dev server.", "Restart the package script that launched it.", l)
		} else if word("python") || word("python3") {
			devServer(&p, "Python service", 76, "Python process listening locally, likely a dev server or tool.", "Restart the Python command.", l)
		} else if strings.Contains(text, "language_server") || strings.Contains(text, "language-server") || strings.Contains(text, "code helper") || strings.Contains(text, "antigravity") {
			p.Identity, p.Kind, p.Source, p.Confidence, p.Risk = "Editor language service", Editor, SrcIDE, 80, Med
			p.StopKind = StopAvoid
			p.StopLabel = "Avoid unless you recognize the editor helper"
			p.Explain = "Editor helper for code intelligence, indexing, or extensions."
			p.Note = "Stopping it can break completions or indexing until the editor reloads."
			p.Warning = "Stop only if you recognize the editor helper."
		} else if formula != "" {
			p.Identity, p.Source, p.Confidence, p.Risk = formula, SrcHomebrew, 82, Med
			p.StopKind, p.StopArg, p.StopLabel = StopBrew, formula, "brew services stop "+formula
			p.Restart = "brew services start " + formula
			p.Explain = "Managed by Homebrew through launchd; killing the PID alone may not be the right action."
			p.Warning = "Managed services may restart unless stopped through their manager."
		} else if system || bgApp {
			p.Identity, p.Source, p.Confidence, p.Risk = pickStr(system, "macOS service", "App helper"), pickSrc(system, SrcMacOS, SrcApp), pick(system, 78, 72), High
			p.StopKind = StopAvoid
			p.StopLabel = "Avoid — owned by " + pickStr(system, "macOS", "an app")
			p.Explain = pickStr(system, "macOS background service listening locally.", "Desktop app helper listening locally.")
			p.Note = "It may restart automatically if the owning app or macOS needs it."
			p.Warning = "Only stop this if you know why it is running."
		} else if wildcard {
			p.Identity, p.Source, p.Confidence, p.Risk = "Open local listener", SrcTerminal, 65, Med
			p.StopLabel = "Send TERM to PID " + itoa(l.PID)
			p.Explain = "Listening on a wildcard address — reachable beyond localhost depending on your firewall."
			p.Note = "Open listener: reachable beyond localhost if your network allows it."
			p.Warning = "Check whether another device depends on this before stopping it."
			p.Restart = "Use the original app or command that started it."
		} else {
			p.Identity, p.Source = displayName(l), SrcTerminal
			p.Explain = "Local listener detected from lsof. No specific profile yet."
			p.Restart = "Use the original app or command that started it."
		}
	}

	if p.Identity == "" {
		p.Identity = displayName(l)
	}
	if p.Source == "" {
		p.Source = sourceFor(l, p, container.name != "", managedBrew, system, bgApp)
	}

	// launchd override: a KeepAlive agent respawns the moment its PID dies,
	// so a plain TERM is a ghost-chase — route the stop through the
	// supervisor instead. Only StopTerm rows convert: brew keeps precedence
	// (brew services IS the launchd front-end for those jobs), docker stops
	// the container not the PID, and StopAvoid rows stay refused.
	if label, ok := env.LaunchdByPID[l.PID]; ok {
		switch p.StopKind {
		case StopTerm:
			target := LaunchdDomainTarget(label)
			p.Source = SrcLaunchd
			p.StopKind, p.StopArg = StopLaunchd, label
			p.StopLabel = "launchctl bootout " + target
			p.Restart = "launchctl bootstrap gui/" + strconv.Itoa(os.Getuid()) + " <path-to-its-plist>"
			p.Note = "launchd agent \"" + label + "\": killing the PID alone just respawns it if KeepAlive is set."
			if p.Risk == Low {
				p.Risk = Med // supervised implies something depends on it staying up
			}
		case StopAvoid:
			// Refused rows stay refused, but name the supervisor — "avoid"
			// without the launchd label leaves the user one clue short of
			// acting deliberately.
			p.Note = strings.TrimSpace(p.Note + " Managed by launchd as \"" + label + "\".")
		}
	}
	return p
}

func database(p *Profile, id string, conf int, explain string, l scan.Listener, formula string, managed bool) {
	p.Identity, p.Kind, p.Confidence, p.Risk = id, Database, conf, High
	p.Source = SrcHomebrew
	p.Explain = explain
	p.Note = id + ": local apps may lose database connections until it is started again."
	p.Warning = "Stopping a database can interrupt running projects."
	if formula != "" {
		p.StopKind, p.StopArg, p.StopLabel = StopBrew, formula, "brew services stop "+formula
		p.Restart = "brew services start " + formula
	} else {
		p.StopKind, p.StopLabel = StopTerm, "Careful TERM to PID "+itoa(l.PID)
		p.Restart = "start " + strings.ToLower(id) + " the way you originally launched it"
		p.Source = SrcTerminal
	}
}

func devServer(p *Profile, id string, conf int, explain, restart string, l scan.Listener) {
	p.Identity, p.Confidence, p.Risk = id, conf, Low
	if p.Kind == "" {
		p.Kind = classifyKind(l)
	}
	p.Source = SrcFramework
	p.StopKind, p.StopLabel = StopTerm, "Send TERM to PID "+itoa(l.PID)
	p.Explain = explain
	p.Note = "Dev server: browser tabs and API calls may show connection refused."
	p.Restart = restart
}

// --- identity helpers ------------------------------------------------------

type interpID struct {
	title, explanation, restart string
	kind                        Kind
	confidence                  int
}

// interpreterIdentity pulls the module/script an interpreter is running, so
// `python3 -m http.server 8000` reads as "http.server", not "Python".
func interpreterIdentity(cmd string) (interpID, bool) {
	if cmd == "" {
		return interpID{}, false
	}
	tokens := strings.Fields(cmd)
	idx := -1
	for i, t := range tokens {
		if isInterpreter(t) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return interpID{}, false
	}
	interp := canonicalInterp(tokens[idx])
	kind := Node
	if strings.HasPrefix(interp, "python") {
		kind = Python
	} else if interp == "ruby" {
		kind = Other
	}
	for i := idx + 1; i < len(tokens); i++ {
		t := tokens[i]
		if t == "-m" && i+1 < len(tokens) {
			mod := tokens[i+1]
			return interpID{
				title:       prettyModule(mod),
				kind:        kind,
				explanation: moduleExplain(mod, interp),
				restart:     interp + " -m " + mod + tail(tokens, i+2),
				confidence:  84,
			}, true
		}
		low := strings.ToLower(t)
		if hasAnySuffix(low, ".py", ".rb", ".mjs", ".js", ".ts") {
			return interpID{
				title:       filepath.Base(t),
				kind:        kind,
				explanation: "Local script `" + filepath.Base(t) + "` listening on a port.",
				restart:     interp + " " + t + tail(tokens, i+1),
				confidence:  82,
			}, true
		}
		if t == "-c" {
			return interpID{}, false
		}
	}
	return interpID{}, false
}

func isInterpreter(token string) bool {
	b := strings.ToLower(filepath.Base(token))
	if b == "node" || b == "deno" || b == "bun" || b == "py" {
		return true
	}
	// python / ruby may carry a version suffix (python3, python3.11, ruby2.7)
	// but a bare HasPrefix also swept in unrelated tools whose name merely
	// starts the same way — python-config, python-build, rubygems — and
	// fabricated an interpreter identity for them. Only accept a trailing
	// version (digits and dots).
	return versionedName(b, "python") || versionedName(b, "ruby")
}

// versionedName reports whether b is exactly name, or name followed only by a
// version suffix of digits and dots.
func versionedName(b, name string) bool {
	if !strings.HasPrefix(b, name) {
		return false
	}
	for _, r := range b[len(name):] {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func canonicalInterp(token string) string {
	b := strings.ToLower(filepath.Base(token))
	if strings.HasPrefix(b, "python") || b == "py" {
		return "python3"
	}
	if strings.HasPrefix(b, "ruby") {
		return "ruby"
	}
	return b
}

func prettyModule(m string) string {
	switch m {
	case "http.server", "SimpleHTTPServer":
		return "http.server"
	case "uvicorn":
		return "Uvicorn"
	case "gunicorn":
		return "Gunicorn"
	case "flask":
		return "Flask"
	case "django":
		return "Django"
	case "streamlit":
		return "Streamlit"
	case "jupyter":
		return "Jupyter"
	case "fastapi":
		return "FastAPI"
	default:
		return m
	}
}

func moduleExplain(m, interp string) string {
	switch m {
	case "http.server", "SimpleHTTPServer":
		return "Python's built-in static file server, serving the working folder over HTTP."
	case "uvicorn":
		return "ASGI server commonly running FastAPI or Starlette apps."
	case "gunicorn":
		return "Production WSGI HTTP server for Python web apps."
	case "streamlit":
		return "Streamlit dashboard running locally."
	case "jupyter":
		return "Jupyter notebook or lab server."
	default:
		return interp + " module `" + m + "` running locally."
	}
}

// wordMatch is the word-boundary substring match: matches "node" in
// "/usr/bin/node" but not in "nodebox" or "node_modules".
func wordMatch(haystack, needle string) bool {
	h, n := strings.ToLower(haystack), strings.ToLower(needle)
	if n == "" {
		return false
	}
	from := 0
	for {
		i := strings.Index(h[from:], n)
		if i < 0 {
			return false
		}
		i += from
		beforeOK := i == 0 || !isWordCont(rune(h[i-1]))
		end := i + len(n)
		afterOK := end >= len(h) || !isWordCont(rune(h[end]))
		if beforeOK && afterOK {
			return true
		}
		from = i + 1
	}
}

func isWordCont(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func classifyKind(l scan.Listener) Kind {
	t := strings.ToLower(l.Command + " " + l.CommandLine)
	switch {
	case wordMatch(t, "redis") || wordMatch(t, "mongod") || strings.Contains(t, "postgres") ||
		strings.Contains(t, "postmaster") || strings.Contains(t, "mysql") || strings.Contains(t, "mariadb"):
		return Database
	case wordMatch(t, "ollama"):
		return AI
	case strings.Contains(t, "language_server") || strings.Contains(t, "language-server") ||
		strings.Contains(t, "code helper") || strings.Contains(t, "antigravity"):
		return Editor
	case wordMatch(t, "python") || wordMatch(t, "python3") || strings.Contains(t, ".py"):
		return Python
	case wordMatch(t, "node") || wordMatch(t, "npm") || wordMatch(t, "pnpm") || wordMatch(t, "yarn") ||
		wordMatch(t, "bun") || strings.Contains(t, ".js") || strings.Contains(t, ".mjs"):
		return Node
	case isSystem(l):
		return System
	case isBackgroundApp(l):
		return App
	default:
		return Other
	}
}

// Match the formula segment specifically — the one after Cellar/ or after a
// homebrew opt/ prefix — so /opt/homebrew/opt/mongodb-community/ yields
// "mongodb-community", not "homebrew".
var cellarRe = regexp.MustCompile(`(?i)(?:/Cellar/|homebrew/opt/|/usr/local/opt/)([a-z0-9@._+-]+)`)

// brewFormula pulls a Homebrew formula name out of a binary path under the
// Homebrew prefix (/opt/homebrew/Cellar/redis/..., /opt/homebrew/opt/redis/...).
func brewFormula(cmd string) string {
	low := strings.ToLower(cmd)
	if !strings.Contains(low, "/cellar/") && !strings.Contains(low, "homebrew/opt/") && !strings.Contains(low, "/usr/local/opt/") {
		return ""
	}
	if m := cellarRe.FindStringSubmatch(cmd); m != nil {
		// Lowercase the captured name: the regex matched against the original
		// case, but brew's own formula names (what BrewStarted is keyed on and
		// what BrewServiceKnown / the `formula == "redis"` checks compare
		// against) are lowercase. A mixed-case path like /Cellar/REDIS/ would
		// otherwise yield "REDIS" and miss every one of those.
		f := strings.ToLower(strings.SplitN(m[1], "@", 2)[0]) // redis@7 -> redis
		if f == "homebrew" {
			return ""
		}
		return f
	}
	return ""
}

func containerFor(l scan.Listener, env Env) dockerContainer {
	for _, port := range l.Ports {
		if c, ok := env.DockerByPort[port]; ok {
			return c
		}
	}
	return dockerContainer{} // a docker helper, if any, with no mapped container name
}

func isSystem(l scan.Listener) bool {
	c := strings.ToLower(l.CommandLine)
	if strings.HasPrefix(c, "/system/") || strings.HasPrefix(c, "/usr/libexec/") ||
		strings.HasPrefix(c, "/usr/sbin/") || strings.HasPrefix(c, "/sbin/") {
		return true
	}
	if l.User == "root" && !strings.Contains(c, "/cellar/") && !strings.Contains(c, "/users/") {
		// Match the daemon name against the EXECUTABLE's basename, not the
		// whole command line — otherwise a root process whose path or args
		// merely contained "launchd"/"mdnsresponder"/etc. (e.g.
		// /tmp/launchd-test/server) was misflagged as a real macOS daemon and
		// forced to High-risk / StopAvoid.
		switch argv0Base(c) {
		case "rapportd", "sharingd", "controlcenter", "launchd", "mdnsresponder":
			return true
		}
	}
	return false
}

// argv0Base returns the lowercased basename of a command line's first token.
func argv0Base(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func isBackgroundApp(l scan.Listener) bool {
	return strings.Contains(strings.ToLower(l.CommandLine), ".app/contents")
}

func hasWildcard(addrs []string) bool {
	for _, a := range addrs {
		if strings.HasPrefix(a, "*:") || strings.HasPrefix(a, "0.0.0.0:") || strings.HasPrefix(a, "[::]:") {
			return true
		}
	}
	return false
}

func sourceFor(l scan.Listener, p Profile, container, brew, system, bgApp bool) Source {
	switch {
	case container:
		return SrcContainer
	case brew:
		return SrcHomebrew
	case system:
		return SrcMacOS
	case p.Kind == Editor:
		return SrcIDE
	case bgApp:
		return SrcApp
	case l.Cwd != "" || l.CommandLine != "":
		return SrcTerminal
	default:
		return SrcUnknown
	}
}

func displayName(l scan.Listener) string {
	if l.Command != "" {
		return l.Command
	}
	if l.CommandLine != "" {
		return filepath.Base(strings.Fields(l.CommandLine)[0])
	}
	return "pid " + itoa(l.PID)
}

// --- tiny helpers ----------------------------------------------------------

func pick(cond bool, a, b int) int {
	if cond {
		return a
	}
	return b
}
func pickStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
func pickSrc(cond bool, a, b Source) Source {
	if cond {
		return a
	}
	return b
}
func tail(tokens []string, from int) string {
	if from >= len(tokens) {
		return ""
	}
	return " " + strings.Join(tokens[from:], " ")
}
func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
