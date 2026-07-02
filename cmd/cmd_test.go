package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
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
