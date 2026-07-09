package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

func sampleRow(port string, pid int, identity string) row {
	return row{
		Listener: scan.Listener{PID: pid, Ports: []string{port}, CommandLine: identity},
		Profile:  intel.Profile{Identity: identity, Risk: intel.Low, Source: intel.SrcTerminal, StopKind: intel.StopTerm, StopLabel: "TERM"},
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"short string unchanged", "node", 10, "node"},
		{"exact fit", "node", 4, "node"},
		{"clipped with ellipsis", "hello world", 8, "hello w…"},
		{"n<1 empty", "anything", 0, ""},
		{"n==1 returns first rune, no ellipsis", "anything", 1, "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncate(c.in, c.n); got != c.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
			}
		})
	}
}

func TestPortCell(t *testing.T) {
	cases := []struct {
		ports []string
		want  string
	}{
		{nil, "-"},
		{[]string{"3000"}, "3000"},
		{[]string{"3000", "3001"}, "3000 +1"},
		{[]string{"3000", "3001", "3002"}, "3000 +2"},
	}
	for _, c := range cases {
		if got := portCell(c.ports); got != c.want {
			t.Errorf("portCell(%v) = %q, want %q", c.ports, got, c.want)
		}
	}
}

func TestMatchRowsByPort(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node"), sampleRow("8080", 200, "django")}
	got := matchRows(rows, "8080")
	if len(got) != 1 || got[0].PID != 200 {
		t.Fatalf("matchRows(8080) = %+v, want the django row", got)
	}
}

func TestMatchRowsFallsBackToPID(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node"), sampleRow("8080", 200, "django")}
	got := matchRows(rows, "100")
	if len(got) != 1 || got[0].Profile.Identity != "node" {
		t.Fatalf("matchRows(100) = %+v, want the node row via PID fallback", got)
	}
}

func TestMatchRowsPortTakesPriorityOverPID(t *testing.T) {
	// "200" is not a port on any row here, but it IS row B's PID — and also
	// happens to be a string a naive implementation might double-match.
	// Port search must exhaust every row first; only fall back to PID
	// when NO row's port matched at all.
	rows := []row{sampleRow("200", 999, "impostor"), sampleRow("8080", 200, "django")}
	got := matchRows(rows, "200")
	if len(got) != 1 || got[0].Profile.Identity != "impostor" {
		t.Fatalf("matchRows(200) = %+v, want only the port-200 row (port beats PID fallback)", got)
	}
}

func TestMatchRowsNoMatch(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node")}
	if got := matchRows(rows, "9999"); len(got) != 0 {
		t.Fatalf("matchRows(9999) = %+v, want no match", got)
	}
	if got := matchRows(rows, "not-a-number"); len(got) != 0 {
		t.Fatalf("matchRows(not-a-number) = %+v, want no match (non-numeric, not a port either)", got)
	}
}

func TestMatchRowsMultiplePortMatches(t *testing.T) {
	// Two listeners can share a port (e.g. IPv4 + IPv6 on the same PID
	// showing up as separate rows, or two unrelated processes racing a
	// port) — every matching row should stop, not just the first found.
	rows := []row{sampleRow("3000", 100, "node-v4"), sampleRow("3000", 100, "node-v6")}
	got := matchRows(rows, "3000")
	if len(got) != 2 {
		t.Fatalf("matchRows(3000) = %+v, want both rows on that port", got)
	}
}

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	rows := []row{sampleRow("3000", 100, "node")}
	if err := printJSON(&buf, rows); err != nil {
		t.Fatalf("printJSON error: %v", err)
	}
	var got []row
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].PID != 100 || got[0].Profile.Identity != "node" {
		t.Fatalf("round-tripped JSON = %+v, want the node row", got)
	}
	if !strings.Contains(buf.String(), "[\n  {\n    \"pid\"") {
		t.Errorf("expected 2-space-per-level indented JSON, got:\n%s", buf.String())
	}
}

func TestPrintTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	printTable(&buf, nil)
	if got := buf.String(); got != "No localhost listeners found.\n" {
		t.Errorf("printTable(nil) = %q", got)
	}
}

func TestDirCell(t *testing.T) {
	home := "/Users/me"
	cases := []struct {
		name string
		cwd  string
		n    int
		want string
	}{
		{"empty is dash", "", 26, "-"},
		{"home abbreviated", "/Users/me/code/app", 26, "~/code/app"},
		{"home itself", "/Users/me", 26, "~"},
		{"home-prefixed sibling NOT abbreviated", "/Users/meister/app", 26, "/Users/meister/app"},
		{"outside home untouched", "/opt/homebrew/var", 26, "/opt/homebrew/var"},
		{"long path truncates from the LEFT", "/Users/me/code/very/deep/project/web", 16, "…eep/project/web"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dirCell(c.cwd, home, c.n); got != c.want {
				t.Errorf("dirCell(%q, %d) = %q, want %q", c.cwd, c.n, got, c.want)
			}
		})
	}
}

func TestPrintTableIncludesDirColumn(t *testing.T) {
	var buf bytes.Buffer
	r := sampleRow("3000", 100, "node")
	r.Cwd = "/opt/projects/webapp"
	printTable(&buf, []row{r})
	out := buf.String()
	if !strings.Contains(out, "DIR") {
		t.Errorf("table header missing DIR column:\n%s", out)
	}
	if !strings.Contains(out, "/opt/projects/webapp") {
		t.Errorf("table row missing the working directory:\n%s", out)
	}
}

func TestPrintTableRows(t *testing.T) {
	var buf bytes.Buffer
	printTable(&buf, []row{sampleRow("3000", 100, "node")})
	out := buf.String()
	for _, want := range []string{"PORT", "PID", "WHAT", "RISK", "OWNER", "STOP WITH", "3000", "100", "node", "low", "terminal", "TERM"} {
		if !strings.Contains(out, want) {
			t.Errorf("printTable output missing %q, got:\n%s", want, out)
		}
	}
}

func TestNewRootRegistersAllSubcommands(t *testing.T) {
	root := newRoot("1.2.3")
	if root.Version != "1.2.3" {
		t.Errorf("root.Version = %q, want 1.2.3", root.Version)
	}
	want := map[string]bool{"list": false, "stop": false, "hold": false, "wait": false, "ready": false, "version": false}
	for _, c := range root.Commands() {
		name := strings.Fields(c.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered on root", name)
		}
	}
}

func TestStopMatchedAllSucceed(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node"), sampleRow("3000", 101, "vite")}
	var out, errOut bytes.Buffer
	stop := func(scan.Listener, intel.Profile) (string, error) { return "sent SIGTERM", nil }
	if err := stopMatched(&out, &errOut, rows, stop); err != nil {
		t.Fatalf("all-success should return nil, got %v", err)
	}
	if strings.Count(out.String(), "✓") != 2 {
		t.Errorf("want two ✓ lines, got:\n%s", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("no failures should mean empty stderr, got:\n%s", errOut.String())
	}
}

func TestStopMatchedPartialFailure(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node"), sampleRow("3000", 101, "stuck")}
	var out, errOut bytes.Buffer
	// The second row fails.
	stop := func(_ scan.Listener, p intel.Profile) (string, error) {
		if p.Identity == "stuck" {
			return "", errors.New("permission denied")
		}
		return "sent SIGTERM", nil
	}
	err := stopMatched(&out, &errOut, rows, stop)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 could not be stopped") {
		t.Fatalf("partial failure error = %v, want '1 of 2 could not be stopped'", err)
	}
	if !strings.Contains(out.String(), "✓ node") {
		t.Errorf("the succeeding row should still print a ✓ line, got:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "✗ stuck") || !strings.Contains(errOut.String(), "permission denied") {
		t.Errorf("the failing row should print a ✗ line with the error, got:\n%s", errOut.String())
	}
}

func TestStopMatchedAllFail(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "a"), sampleRow("3000", 101, "b")}
	var out, errOut bytes.Buffer
	stop := func(scan.Listener, intel.Profile) (string, error) { return "", errors.New("nope") }
	err := stopMatched(&out, &errOut, rows, stop)
	if err == nil || !strings.Contains(err.Error(), "2 of 2 could not be stopped") {
		t.Fatalf("all-fail error = %v, want '2 of 2 could not be stopped'", err)
	}
	if strings.Contains(out.String(), "✓") {
		t.Errorf("no row succeeded, stdout should have no ✓, got:\n%s", out.String())
	}
}

func TestWithinDir(t *testing.T) {
	cases := []struct {
		cwd, dir string
		want     bool
	}{
		{"/proj/web", "/proj", true},         // nested
		{"/proj", "/proj", true},             // exact
		{"/proj/web/src", "/proj/web", true}, // deeper
		{"/projector", "/proj", false},       // sibling prefix — must NOT match
		{"/proj/web", "/proj/", true},        // trailing slash normalized by Clean
		{"/other", "/proj", false},           // unrelated
		{"", "/proj", false},                 // no cwd
		{"/proj", "", false},                 // no dir
	}
	for _, c := range cases {
		if got := withinDir(c.cwd, c.dir); got != c.want {
			t.Errorf("withinDir(%q, %q) = %v, want %v", c.cwd, c.dir, got, c.want)
		}
	}
}

func TestMatchDir(t *testing.T) {
	rows := []row{
		{Listener: scan.Listener{PID: 1, Cwd: "/proj/web"}, Profile: intel.Profile{Identity: "web"}},
		{Listener: scan.Listener{PID: 2, Cwd: "/proj/api"}, Profile: intel.Profile{Identity: "api"}},
		{Listener: scan.Listener{PID: 3, Cwd: "/elsewhere"}, Profile: intel.Profile{Identity: "other"}},
		{Listener: scan.Listener{PID: 4, Cwd: ""}, Profile: intel.Profile{Identity: "nocwd"}},
	}
	got := matchDir(rows, "/proj")
	if len(got) != 2 {
		t.Fatalf("matchDir(/proj) matched %d rows, want 2: %+v", len(got), got)
	}
	names := map[string]bool{got[0].Profile.Identity: true, got[1].Profile.Identity: true}
	if !names["web"] || !names["api"] {
		t.Errorf("matchDir matched %v, want web + api", names)
	}
}

func TestMatchDirNoMatch(t *testing.T) {
	rows := []row{{Listener: scan.Listener{PID: 1, Cwd: "/somewhere/else"}}}
	if got := matchDir(rows, "/proj"); len(got) != 0 {
		t.Errorf("matchDir found %d, want 0", len(got))
	}
}

// Regression: `le stop --dir .` (and any relative path) must resolve to an
// absolute path before matching against lsof's always-absolute cwds. It used
// to compare "." against absolute cwds and match nothing.
func TestMatchDirRelativePathResolvesToCwd(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// resolvePath applies EvalSymlinks, so match against the resolved cwd to
	// stay correct on symlinked temp roots (e.g. macOS /var -> /private/var).
	resolved := resolvePath(cwd)
	rows := []row{
		{Listener: scan.Listener{PID: 1, Cwd: resolved}, Profile: intel.Profile{Identity: "here"}},
		{Listener: scan.Listener{PID: 2, Cwd: "/somewhere/unrelated"}, Profile: intel.Profile{Identity: "there"}},
	}
	got := matchDir(rows, ".")
	if len(got) != 1 || got[0].Profile.Identity != "here" {
		t.Fatalf(`matchDir(".") = %+v, want the row whose cwd is the current dir`, got)
	}
}

func TestWithinDirRootMatchesEverything(t *testing.T) {
	// `--dir /` should match every absolute cwd, not none (the "//" prefix bug).
	if !withinDir("/Users/me/project", "/") {
		t.Error(`withinDir("/Users/me/project", "/") = false, want true`)
	}
	if !withinDir("/", "/") {
		t.Error(`withinDir("/", "/") = false, want true`)
	}
}

func TestFilterRows(t *testing.T) {
	rows := []row{
		sampleRow("3000", 100, "node"),
		sampleRow("8000", 200, "django"),
		{Listener: scan.Listener{PID: 300, Ports: []string{"5432"}, Cwd: "/proj/db"},
			Profile: intel.Profile{Identity: "Postgres", Source: intel.SrcHomebrew}},
	}
	cases := []struct {
		q     string
		want  int
		check string // an identity that must be present (empty = skip)
	}{
		{"node", 1, "node"},         // identity/command match
		{"8000", 1, "django"},       // port match
		{"/proj/db", 1, "Postgres"}, // cwd match
		{"homebrew", 1, "Postgres"}, // source match
		{"", 3, ""},                 // empty filter keeps all
		{"nothingmatches", 0, ""},   // no match
	}
	for _, c := range cases {
		got := filterRows(rows, c.q)
		if len(got) != c.want {
			t.Errorf("filterRows(%q) matched %d, want %d", c.q, len(got), c.want)
			continue
		}
		if c.check != "" && got[0].Profile.Identity != c.check {
			t.Errorf("filterRows(%q) top row = %q, want %q", c.q, got[0].Profile.Identity, c.check)
		}
	}
}

func TestFilterRowsEmptyIsNonNil(t *testing.T) {
	// No match must return a non-nil empty slice so `le list <f> --json`
	// emits [] (not null), matching the unfiltered zero-listener case.
	rows := []row{sampleRow("3000", 100, "node")}
	got := filterRows(rows, "nomatch")
	if got == nil {
		t.Fatal("filterRows with no match returned nil; want a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("filterRows with no match returned %d rows, want 0", len(got))
	}
	var buf bytes.Buffer
	if err := printJSON(&buf, got); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("empty filtered JSON = %q, want []", strings.TrimSpace(buf.String()))
	}
}

func TestPreviewMatched(t *testing.T) {
	rows := []row{
		{Listener: scan.Listener{PID: 100}, Profile: intel.Profile{Identity: "node", StopLabel: "Send TERM to PID 100"}},
		{Listener: scan.Listener{PID: 200}, Profile: intel.Profile{Identity: "redis", StopLabel: "brew services stop redis"}},
	}
	var buf bytes.Buffer
	previewMatched(&buf, rows)
	out := buf.String()
	if strings.Count(out, "would stop") != 2 {
		t.Errorf("expected two 'would stop' lines, got:\n%s", out)
	}
	// The preview must name the process, pid, and the strategy — everything the
	// user needs to sanity-check a sweep before running it for real.
	for _, want := range []string{"node", "100", "Send TERM to PID 100", "redis", "200", "brew services stop redis"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q, got:\n%s", want, out)
		}
	}
}

func TestStopMatchedJSONAllSucceed(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node"), sampleRow("3001", 101, "vite")}
	var buf bytes.Buffer
	stop := func(scan.Listener, intel.Profile) (string, error) { return "sent SIGTERM", nil }
	if err := stopMatchedJSON(&buf, rows, stop); err != nil {
		t.Fatalf("all-success should return nil, got %v", err)
	}
	var got []stopResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 2 || !got[0].OK || !got[1].OK || got[0].Action != "sent SIGTERM" {
		t.Fatalf("results = %+v, want two ok rows with the stop message as action", got)
	}
	if got[0].DryRun {
		t.Error("a real stop must not be marked dryRun")
	}
}

func TestStopMatchedJSONPartialFailure(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node"), sampleRow("3001", 101, "stuck")}
	var buf bytes.Buffer
	stop := func(_ scan.Listener, p intel.Profile) (string, error) {
		if p.Identity == "stuck" {
			return "", errors.New("permission denied")
		}
		return "sent SIGTERM", nil
	}
	err := stopMatchedJSON(&buf, rows, stop)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 could not be stopped") {
		t.Fatalf("partial failure error = %v, want '1 of 2 could not be stopped'", err)
	}
	var got []stopResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON despite the failure: %v\n%s", err, buf.String())
	}
	// The failing row must still be IN the JSON, with ok=false and the error.
	if len(got) != 2 || got[1].OK || !strings.Contains(got[1].Error, "permission denied") {
		t.Fatalf("results = %+v, want the failed row present with ok=false and its error", got)
	}
	if !got[0].OK {
		t.Error("the succeeding row should still be ok=true")
	}
}

func TestPreviewMatchedJSON(t *testing.T) {
	rows := []row{sampleRow("3000", 100, "node")}
	var buf bytes.Buffer
	if err := previewMatchedJSON(&buf, rows); err != nil {
		t.Fatal(err)
	}
	var got []stopResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || !got[0].DryRun || !got[0].OK || got[0].Action != "TERM" {
		t.Fatalf("results = %+v, want one dryRun=true ok row with the StopLabel as action", got)
	}
}

func TestStopCmdHasJSONFlag(t *testing.T) {
	if stopCmd().Flags().Lookup("json") == nil {
		t.Error("stop command missing --json flag")
	}
}

func TestListCmdHasJSONFlagAndAlias(t *testing.T) {
	c := listCmd()
	if c.Flags().Lookup("json") == nil {
		t.Error("list command missing --json flag")
	}
	found := false
	for _, a := range c.Aliases {
		if a == "ls" {
			found = true
		}
	}
	if !found {
		t.Error(`list command missing "ls" alias`)
	}
}

func TestCPUListCell(t *testing.T) {
	cases := []struct {
		cpu  float64
		want string
	}{
		{818.8, "819% ●"}, // runaway → hot glyph (>= CPUHotPct 200)
		{200.0, "200% ●"}, // exactly hot
		{100.0, "100% ▲"}, // one core → warm glyph (>= CPUWarmPct 50)
		{50.0, "50% ▲"},   // exactly warm
		{12.0, "12%"},     // busy but not notable
		{0.2, "·"},        // rounds to 0 → dot, not "0%"
		{0.0, "·"},        // idle
	}
	for _, c := range cases {
		if got := cpuListCell(c.cpu); got != c.want {
			t.Errorf("cpuListCell(%v) = %q, want %q", c.cpu, got, c.want)
		}
	}
}
