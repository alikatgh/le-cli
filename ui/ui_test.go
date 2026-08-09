package ui

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// Drives the model headlessly through resize, data, navigation, filtering, and
// the stop-confirm flow. Catches the panics a TTY-less CI would otherwise miss
// (slice bounds in the row renderer, cursor clamps, negative widths).
func key(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func sampleRows() []Row {
	return []Row{
		{L: scan.Listener{PID: 101, Ports: []string{"3000"}, Command: "node", CommandLine: "node app.js", Cwd: "/code/web"},
			P: intel.Profile{Identity: "Node service", Kind: intel.Node, Source: intel.SrcTerminal, Risk: intel.Low, StopKind: intel.StopTerm, Confidence: 78, StopLabel: "TERM"}},
		{L: scan.Listener{PID: 102, Ports: []string{"27017"}, Command: "mongod", CommandLine: "/opt/homebrew/opt/mongodb-community/bin/mongod"},
			P: intel.Profile{Identity: "MongoDB", Kind: intel.Database, Source: intel.SrcHomebrew, Risk: intel.High, StopKind: intel.StopBrew, StopArg: "mongodb-community", Confidence: 95, Warning: "Stopping a database can interrupt running projects."}},
		{L: scan.Listener{PID: 103, Ports: []string{"5000"}, Command: "ControlCenter", CommandLine: "/System/Library/ControlCenter"},
			P: intel.Profile{Identity: "macOS service", Kind: intel.System, Source: intel.SrcMacOS, Risk: intel.High, StopKind: intel.StopAvoid, Confidence: 78}},
	}
}

func drive(t *testing.T, m tea.Model, keys ...string) tea.Model {
	t.Helper()
	for _, k := range keys {
		m, _ = m.Update(key(k))
		if s := m.(model).View(); s == "" {
			t.Fatalf("empty view after key %q", k)
		}
	}
	return m
}

func TestModelLifecycle(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})

	if !strings.Contains(m.(model).View(), "MongoDB") {
		t.Fatal("expected MongoDB row in view")
	}

	// navigate, jump, help toggle
	m = drive(t, m, "j", "j", "k", "G", "g", "?", "?")

	// filter down to mongo, then clear
	m, _ = m.Update(key("/"))
	for _, r := range "mongo" {
		m, _ = m.Update(key(string(r)))
	}
	if got := len(m.(model).view); got != 1 {
		t.Fatalf("filter 'mongo' => %d rows, want 1", got)
	}
	m, _ = m.Update(key("esc"))
	if got := len(m.(model).view); got != 3 {
		t.Fatalf("after clearing filter => %d rows, want 3", got)
	}

	// stop-confirm on a TERM-able row, then decline
	m, _ = m.Update(key("x"))
	if !m.(model).confirm {
		t.Fatal("expected confirm dialog after x")
	}
	m, _ = m.Update(key("n"))
	if m.(model).confirm {
		t.Fatal("confirm should be dismissed after n")
	}

	// an avoid-row must refuse to confirm. Find it by property, not a
	// hardcoded index — which row lands where depends on the active sort.
	mm := m.(model)
	mm.cursor = -1
	for i, r := range mm.view {
		if r.P.StopKind == intel.StopAvoid {
			mm.cursor = i
			break
		}
	}
	if mm.cursor < 0 {
		t.Fatal("no StopAvoid row in sample data")
	}
	var mi tea.Model = mm
	mi, _ = mi.Update(key("x"))
	if mi.(model).confirm {
		t.Fatal("avoid-row should not open a confirm")
	}
}

func TestNarrowWindowDoesNotPanic(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 38, Height: 12})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})
	_ = drive(t, m, "j", "G", "x", "esc")
}

func TestEmptyAndLoadingRender(t *testing.T) {
	var m tea.Model = New(Options{})
	if m.(model).View() != "starting le…" {
		t.Fatal("expected pre-size placeholder")
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(scannedMsg{rows: nil, at: time.Now()})
	if v := m.(model).View(); v == "" {
		t.Fatal("empty-state view should not be blank")
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
		{"exact width fits", "node", 4, "node"},
		{"ascii clipped to budget", "hello world", 8, "hello w…"},
		{"n<1 returns empty", "anything", 0, ""},
		{"n==1 returns just ellipsis", "anything", 1, "…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := truncate(c.in, c.n); got != c.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
			}
		})
	}
}

// A prior fix made truncate rune-safe (no more slicing a multi-byte UTF-8
// character in half) but still bounded the cut by RUNE COUNT, not display
// width. A wide rune (CJK, some emoji) costs 2 terminal columns, so a
// Unicode container/directory name could render at roughly 2x the caller's
// column budget despite passing the rune-count check.
func TestTruncateRespectsDisplayWidthForWideRunes(t *testing.T) {
	in := "日本語のコンテナ名前" // 10 runes, each 2 columns wide = 20 columns
	got := truncate(in, 8)
	if w := lipgloss.Width(got); w > 8 {
		t.Fatalf("truncate(%q, 8) = %q, display width %d, want <= 8", in, got, w)
	}
	if want := "日本語…"; got != want {
		t.Errorf("truncate(%q, 8) = %q, want %q", in, got, want)
	}
}

// Regression test: scanCmd() can have multiple scans in flight at once (tick,
// manual refresh, post-stop refresh), and Bubble Tea delivers their results
// in completion order, not start order — so an older scan can land after a
// newer one already has. Applying it must not roll the table back.
func TestScannedMsgIgnoresStaleResult(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	newer := time.Now()
	older := newer.Add(-time.Minute)

	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: newer})
	if got := len(m.(model).all); got != 3 {
		t.Fatalf("after newer scan: %d rows, want 3", got)
	}

	m, _ = m.Update(scannedMsg{rows: nil, at: older})
	if got := len(m.(model).all); got != 3 {
		t.Fatalf("after stale scan: %d rows, want 3 (stale result should be dropped)", got)
	}
	if !m.(model).lastScan.Equal(newer) {
		t.Fatalf("lastScan = %v, want unchanged at %v", m.(model).lastScan, newer)
	}
}

// Regression: opening the confirm dialog must PIN the selected row, so a
// background scan that reorders/replaces the view can't retarget the pending
// stop to a different process when the user presses y.
func TestConfirmPinsRowAgainstBackgroundScan(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})

	// Cursor 0 is the Node service (PID 101, a stoppable TERM row) after the
	// default port-ascending sort. Open the confirm dialog on it.
	m, _ = m.Update(key("x"))
	if !m.(model).confirm {
		t.Fatal("expected confirm after x")
	}
	pinned := m.(model).confirmed.L.PID
	if pinned != 101 {
		t.Fatalf("pinned PID = %d, want 101 (the Node row)", pinned)
	}

	// A background scan lands with PID 101 gone — the view rebuilds and the
	// cursor now sits over a DIFFERENT process.
	remaining := sampleRows()[1:] // drop the Node row (101)
	m, _ = m.Update(scannedMsg{rows: remaining, at: time.Now().Add(time.Second)})

	if sel, ok := m.(model).selected(); !ok || sel.L.PID == 101 {
		t.Fatalf("after the scan, selected() should be a different row, got PID %d", sel.L.PID)
	}
	// The pinned row must NOT have changed — that's what y will act on.
	if got := m.(model).confirmed.L.PID; got != 101 {
		t.Errorf("confirmed row = PID %d after background scan, want it still pinned to 101", got)
	}
}

// Regression: a click in the empty space BELOW the last visible row must not
// select an off-screen row. With more rows than fit, only rendered rows are
// clickable.
func TestMouseClickBelowVisibleRowsIsIgnored(t *testing.T) {
	rows := make([]Row, 20)
	for i := range rows {
		rows[i] = Row{
			L: scan.Listener{PID: 100 + i, Ports: []string{strconv.Itoa(3000 + i)}},
			P: intel.Profile{Identity: "svc", Source: intel.SrcTerminal, Risk: intel.Low, StopKind: intel.StopTerm},
		}
	}
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 16}) // short window -> few visible rows
	m, _ = m.Update(scannedMsg{rows: rows, at: time.Now()})

	before := m.(model).cursor
	// Y far below the visible table (data rows start at Y=2; a short window
	// shows only a handful) — must be ignored, cursor unchanged.
	m, _ = m.Update(tea.MouseMsg{Y: 50, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if got := m.(model).cursor; got != before {
		t.Errorf("click far below the visible rows moved cursor to %d (was %d); want unchanged", got, before)
	}
}

func TestStopCommand(t *testing.T) {
	cases := []struct {
		name string
		row  Row
		want string
		ok   bool
	}{
		{"term", Row{L: scan.Listener{PID: 42}, P: intel.Profile{StopKind: intel.StopTerm}}, "kill -TERM 42", true},
		{"brew", Row{P: intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"}}, "brew services stop redis", true},
		{"docker", Row{P: intel.Profile{StopKind: intel.StopDocker, StopArg: "web"}}, "docker stop web", true},
		{"avoid refuses", Row{P: intel.Profile{StopKind: intel.StopAvoid}}, "", false},
	}
	for _, c := range cases {
		got, ok := stopCommand(c.row)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: stopCommand = (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestOpenKeyOpensFirstPort(t *testing.T) {
	restore := scan.SetTLSProbeForTesting(func(string) bool { return false })
	defer restore()
	var opened string
	orig := openURL
	openURL = func(u string) error { opened = u; return nil }
	defer func() { openURL = orig }()

	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})
	m, _ = m.Update(key("o"))

	if opened != "http://localhost:3000/" {
		t.Errorf("opened %q, want http://localhost:3000/", opened)
	}
	if mm := m.(model); mm.flashErr || !strings.Contains(mm.flash, "opened") {
		t.Errorf("flash = %q (err=%v), want success flash", mm.flash, mm.flashErr)
	}
}

func TestOpenKeyDetectsHTTPS(t *testing.T) {
	// A TLS-speaking dev server (vite --https, caddy) must open as https://.
	restore := scan.SetTLSProbeForTesting(func(string) bool { return true })
	defer restore()
	var opened string
	orig := openURL
	openURL = func(u string) error { opened = u; return nil }
	defer func() { openURL = orig }()

	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})
	_, _ = m.Update(key("o"))

	if opened != "https://localhost:3000/" {
		t.Errorf("opened %q, want https://localhost:3000/", opened)
	}
}

func TestCopyPickerStopCommand(t *testing.T) {
	var copied string
	orig := copyToClipboard
	copyToClipboard = func(s string) error { copied = s; return nil }
	defer func() { copyToClipboard = orig }()

	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})

	m, _ = m.Update(key("c")) // open the copy picker (cursor 0 = Node, PID 101, StopTerm)
	if !m.(model).copyMenu {
		t.Fatal("pressing c should open the copy picker")
	}
	m, _ = m.Update(key("s")) // pick: stop command
	if copied != "kill -TERM 101" {
		t.Errorf("copied %q, want kill -TERM 101", copied)
	}
	if m.(model).copyMenu {
		t.Error("picker should close after a selection")
	}

	// An avoid row must refuse and copy nothing.
	copied = ""
	mm := m.(model)
	for i, r := range mm.view {
		if r.P.StopKind == intel.StopAvoid {
			mm.cursor = i
			break
		}
	}
	var mi tea.Model = mm
	mi, _ = mi.Update(key("c"))
	mi, _ = mi.Update(key("s"))
	if copied != "" {
		t.Errorf("avoid row copied %q, want nothing", copied)
	}
	if got := mi.(model); !got.flashErr {
		t.Error("avoid row should flash an error")
	}
}

func TestCopyPickerURLCurlLsof(t *testing.T) {
	restore := scan.SetTLSProbeForTesting(func(string) bool { return false })
	defer restore()
	var copied string
	orig := copyToClipboard
	copyToClipboard = func(s string) error { copied = s; return nil }
	defer func() { copyToClipboard = orig }()

	base := func() tea.Model {
		var m tea.Model = New(Options{})
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})
		return m
	}

	// cursor 0 = Node on port 3000. Each picker key yanks the matching value.
	cases := []struct{ pick, want string }{
		{"u", "http://localhost:3000/"},
		{"r", "curl 'http://localhost:3000/'"},
		{"l", "lsof -nP -iTCP:3000 -sTCP:LISTEN"},
	}
	for _, c := range cases {
		copied = ""
		m := base()
		m, _ = m.Update(key("c"))
		m, _ = m.Update(key(c.pick))
		if copied != c.want {
			t.Errorf("pick %q copied %q, want %q", c.pick, copied, c.want)
		}
		if m.(model).copyMenu {
			t.Errorf("pick %q left the picker open", c.pick)
		}
	}

	// esc cancels the picker without copying.
	copied = ""
	m := base()
	m, _ = m.Update(key("c"))
	m, _ = m.Update(key("esc"))
	if copied != "" {
		t.Errorf("esc copied %q, want nothing", copied)
	}
	if m.(model).copyMenu {
		t.Error("esc should close the picker")
	}
}

func TestOSC8LinkHasZeroDisplayWidth(t *testing.T) {
	// The clickable-port link MUST measure the same as its plain text, or the
	// escape shifts every column after the port. If this fails, OSC 8 is not
	// zero-width in this lipgloss/x-ansi version — back the hyperlink out.
	plain := "3000"
	linked := osc8(plain, "http://localhost:3000/")
	if got, want := lipgloss.Width(linked), lipgloss.Width(plain); got != want {
		t.Fatalf("osc8 link width = %d, want %d (same as plain) — OSC 8 not zero-width, breaks alignment", got, want)
	}
	if got := lipgloss.Width(padRight(linked, 8)); got != 8 {
		t.Fatalf("padRight(link, 8) width = %d, want 8", got)
	}
}

func TestMouse(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})

	m, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.(model).cursor != 1 {
		t.Fatalf("wheel down => cursor %d, want 1", m.(model).cursor)
	}
	m, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if m.(model).cursor != 0 {
		t.Fatalf("wheel up => cursor %d, want 0", m.(model).cursor)
	}
	// Y=2 is the first data row; Y=4 selects index 2.
	m, _ = m.Update(tea.MouseMsg{Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.(model).cursor != 2 {
		t.Fatalf("click Y=4 => cursor %d, want 2", m.(model).cursor)
	}
	if m.(model).View() == "" {
		t.Fatal("empty view after mouse input")
	}
}

// sortRows is a fixture where every column sorts into a different order, so
// a comparator wired to the wrong field (or an alphabetical string compare
// where a severity rank belongs) shows up as a wrong sequence, not a
// coincidentally-correct one.
func sortRows() []Row {
	return []Row{
		{L: scan.Listener{PID: 50, Ports: []string{"9000"}},
			P: intel.Profile{Identity: "alpha", Risk: intel.High, Source: intel.SrcApp, StopKind: intel.StopTerm}},
		{L: scan.Listener{PID: 800, Ports: []string{"3000"}},
			P: intel.Profile{Identity: "charlie", Risk: intel.Low, Source: intel.SrcHomebrew, StopKind: intel.StopTerm}},
		{L: scan.Listener{PID: 200, Ports: []string{"6000"}},
			P: intel.Profile{Identity: "bravo", Risk: intel.Med, Source: intel.SrcFramework, StopKind: intel.StopTerm}},
	}
}

func identities(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.P.Identity
	}
	return out
}

func assertOrder(t *testing.T, m tea.Model, want ...string) {
	t.Helper()
	got := identities(m.(model).view)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("view order = %v, want %v", got, want)
	}
}

func TestSortColumns(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sortRows(), at: time.Now()})

	// Default: port ascending (9000 alpha, 3000 charlie, 6000 bravo).
	assertOrder(t, m, "charlie", "bravo", "alpha")

	// "1" re-selects the already-active port column => reverses it.
	m, _ = m.Update(key("1"))
	assertOrder(t, m, "alpha", "bravo", "charlie")
	m, _ = m.Update(key("1"))
	assertOrder(t, m, "charlie", "bravo", "alpha")

	// "2" switches to PID (50, 800, 200) => ascending regardless of the
	// previous column's direction.
	m, _ = m.Update(key("2"))
	assertOrder(t, m, "alpha", "bravo", "charlie")
	m, _ = m.Update(key("2"))
	assertOrder(t, m, "charlie", "bravo", "alpha")

	// "3" sorts by identity, case-insensitively alphabetical.
	m, _ = m.Update(key("3"))
	assertOrder(t, m, "alpha", "bravo", "charlie")

	// "4" sorts by risk SEVERITY (low < medium < high), not alphabetically
	// ("high" < "low" < "medium" would give the wrong order).
	m, _ = m.Update(key("4"))
	assertOrder(t, m, "charlie", "bravo", "alpha")

	// "5" sorts by owner/source string.
	m, _ = m.Update(key("5"))
	assertOrder(t, m, "alpha", "bravo", "charlie")
}

// Regression test: m.view used to alias m.all's backing array in the
// no-filter case, so sorting m.view (a slice op) silently reordered m.all
// too. Confirm a sort leaves m.all in its original scan order.
func TestSortDoesNotReorderAll(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sortRows(), at: time.Now()})
	m, _ = m.Update(key("4")) // reorders m.view by risk

	want := identities(sortRows())
	if got := identities(m.(model).all); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("m.all = %v, want unchanged scan order %v", got, want)
	}
}

// The active sort survives filtering: applyFilter rebuilds m.view from
// scratch on every keystroke, so the sort has to be reapplied each time, not
// just once at scan time.
func TestSortSurvivesFilter(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sortRows(), at: time.Now()})
	m, _ = m.Update(key("3")) // sort by identity ascending: alpha, bravo, charlie

	m, _ = m.Update(key("/"))
	for _, r := range "a" { // matches all three (alpha, bravo, charlie all contain "a")
		m, _ = m.Update(key(string(r)))
	}
	assertOrder(t, m, "alpha", "bravo", "charlie")
}

func TestDirCellWidthAwareLeftTruncation(t *testing.T) {
	home := "/Users/me"
	if got := dirCell("", home, 24); got != "-" {
		t.Errorf("empty cwd = %q, want -", got)
	}
	if got := dirCell("/Users/me/code/app", home, 24); got != "~/code/app" {
		t.Errorf("home abbrev = %q, want ~/code/app", got)
	}
	// Left-truncated: the trailing components must survive.
	got := dirCell("/opt/very/long/path/to/project/web", home, 16)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "project/web") {
		t.Errorf("left truncation = %q, want …-prefixed with the tail intact", got)
	}
	// Display-width, not rune count: CJK path components are 2 columns each.
	wide := dirCell("/proj/日本語のフォルダ名前です", home, 10)
	if w := lipgloss.Width(wide); w > 10 {
		t.Errorf("CJK path rendered at %d columns, want <= 10 (%q)", w, wide)
	}
}

func TestSortByDirKey6(t *testing.T) {
	rows := []Row{
		{L: scan.Listener{PID: 1, Ports: []string{"3000"}, Cwd: "/zzz/proj"}, P: intel.Profile{Identity: "last", StopKind: intel.StopTerm}},
		{L: scan.Listener{PID: 2, Ports: []string{"4000"}, Cwd: "/aaa/proj"}, P: intel.Profile{Identity: "first", StopKind: intel.StopTerm}},
	}
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m, _ = m.Update(scannedMsg{rows: rows, at: time.Now()})

	m, _ = m.Update(key("6"))
	assertOrder(t, m, "first", "last") // /aaa before /zzz
	m, _ = m.Update(key("6"))
	assertOrder(t, m, "last", "first") // reversed
}

func TestDirColumnIsWidthAdaptive(t *testing.T) {
	rows := []Row{{
		L: scan.Listener{PID: 1, Ports: []string{"3000"}, Cwd: "/code/webapp"},
		P: intel.Profile{Identity: "node", Source: intel.SrcTerminal, Risk: intel.Low, StopKind: intel.StopTerm},
	}}
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m, _ = m.Update(scannedMsg{rows: rows, at: time.Now()})
	if v := m.(model).View(); !strings.Contains(v, "DIR") || !strings.Contains(v, "/code/webapp") {
		t.Errorf("wide window should show the DIR column with the cwd, got:\n%s", v)
	}

	// Narrow: the DIR column header disappears (the detail pane's lowercase
	// "dir" line is unaffected — it always showed the cwd).
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if v := m.(model).View(); strings.Contains(v, "DIR") {
		t.Errorf("narrow window should hide the DIR column header, got:\n%s", v)
	}
}

func TestSortHeaderShowsDirection(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sortRows(), at: time.Now()})

	if v := m.(model).View(); !strings.Contains(v, "PORT^") {
		t.Errorf("expected default ascending port indicator PORT^ in view, got:\n%s", v)
	}
	m, _ = m.Update(key("4"))
	if v := m.(model).View(); !strings.Contains(v, "RISK^") {
		t.Errorf("expected RISK^ after pressing 4, got:\n%s", v)
	}
	m, _ = m.Update(key("4"))
	if v := m.(model).View(); !strings.Contains(v, "RISKv") {
		t.Errorf("expected RISKv after pressing 4 twice, got:\n%s", v)
	}
}

func TestCPUCellAndColor(t *testing.T) {
	if cpuCell(0.2) != "·" {
		t.Errorf("sub-1%% should render ·, got %q", cpuCell(0.2))
	}
	if cpuCell(818.8) != "819%" {
		t.Errorf("cpuCell(818.8) = %q, want 819%%", cpuCell(818.8))
	}
	if cpuColor(250) != red {
		t.Error(">= CPUHotPct must be red")
	}
	if cpuColor(60) != yellow {
		t.Error(">= CPUWarmPct must be yellow")
	}
	if cpuColor(5) != subtle {
		t.Error("idle must be subtle")
	}
}

func TestFmtMem(t *testing.T) {
	if got := fmtMem(0); got != "—" {
		t.Errorf("fmtMem(0) = %q, want —", got)
	}
	if got := fmtMem(204800); got != "200 MB" {
		t.Errorf("fmtMem(204800 KB) = %q, want 200 MB", got)
	}
	if got := fmtMem(2 * 1024 * 1024); got != "2.0 GB" {
		t.Errorf("fmtMem(2GB) = %q, want 2.0 GB", got)
	}
}

// --- row actions (parity with the mac app's clickable rows) ---

// withRow drives the model until the named row is selected. By identity, not
// index: the table sorts by port, so sampleRows' order is NOT the view order
// and an index-based helper silently tests the wrong row.
func withRow(t *testing.T, identity string) tea.Model {
	t.Helper()
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})
	for i := 0; i < len(sampleRows()); i++ {
		if r, ok := m.(model).selected(); ok && r.P.Identity == identity {
			return m
		}
		m, _ = m.Update(key("j"))
	}
	t.Fatalf("no row named %q in the view", identity)
	return m
}

func TestRevealKeyRevealsWorkingDirectory(t *testing.T) {
	var revealed string
	orig := revealPath
	revealPath = func(p string) error { revealed = p; return nil }
	defer func() { revealPath = orig }()

	m := withRow(t, "Node service") // cwd /code/web
	m, _ = m.Update(key("F"))

	if revealed != "/code/web" {
		t.Errorf("revealed %q, want /code/web", revealed)
	}
	if mm := m.(model); mm.flashErr || !strings.Contains(mm.flash, "revealed") {
		t.Errorf("flash = %q (err=%v), want a success flash", mm.flash, mm.flashErr)
	}
}

// The row that prompted all this: an app helper whose cwd is a useless
// container path. Nothing on disk matches the fake test path, so there is
// nothing to reveal — and the refusal must say so rather than silently
// revealing the wrong thing.
func TestRevealKeyWithoutTargetExplainsItself(t *testing.T) {
	called := false
	orig := revealPath
	revealPath = func(string) error { called = true; return nil }
	defer func() { revealPath = orig }()

	m := withRow(t, "macOS service") // StopAvoid, no cwd, non-existent binary path
	m, _ = m.Update(key("F"))

	if called {
		t.Error("must not try to reveal a path that doesn't resolve")
	}
	if mm := m.(model); !mm.flashErr || !strings.Contains(mm.flash, "nothing to reveal") {
		t.Errorf("flash = %q (err=%v), want an explanatory error", mm.flash, mm.flashErr)
	}
}

// revealTarget prefers the binary over the cwd for an avoid row, because a
// helper's cwd is a container path and its binary is the actual answer to
// "what is this?". Verified against a real path so the on-disk check passes.
func TestRevealTargetPrefersBinaryForAppHelper(t *testing.T) {
	helper := Row{
		L: scan.Listener{PID: 9, CommandLine: "/bin/sh --serve", Cwd: "/tmp"},
		P: intel.Profile{Identity: "App helper", StopKind: intel.StopAvoid},
	}
	got, ok := revealTarget(helper)
	if !ok || got != "/bin/sh" {
		t.Errorf("revealTarget = (%q, %v), want (/bin/sh, true)", got, ok)
	}

	// A normal row keeps its working directory.
	normal := Row{
		L: scan.Listener{PID: 9, CommandLine: "/bin/sh app.js", Cwd: "/code/web"},
		P: intel.Profile{Identity: "Node", StopKind: intel.StopTerm},
	}
	if got, ok := revealTarget(normal); !ok || got != "/code/web" {
		t.Errorf("revealTarget = (%q, %v), want (/code/web, true)", got, ok)
	}
}

func TestExecutablePathHandlesSpacesAndGivesUp(t *testing.T) {
	// Longest existing prefix wins, so a path with spaces still resolves.
	dir := t.TempDir()
	spaced := dir + "/My App"
	if err := os.WriteFile(spaced, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := executablePath(spaced + " --flag"); got != spaced {
		t.Errorf("executablePath = %q, want %q", got, spaced)
	}
	// Nothing on disk → no guess.
	if got := executablePath("/definitely/not/here --flag"); got != "" {
		t.Errorf("executablePath = %q, want empty for a non-existent path", got)
	}
	// Not an absolute path → not a path at all.
	if got := executablePath("node app.js"); got != "" {
		t.Errorf("executablePath = %q, want empty for a bare command", got)
	}
}

func TestTerminalKeyNeedsAWorkingDirectory(t *testing.T) {
	var opened string
	orig := openTerminalAt
	openTerminalAt = func(d string) error { opened = d; return nil }
	defer func() { openTerminalAt = orig }()

	m := withRow(t, "Node service")
	_, _ = m.Update(key("T"))
	if opened != "/code/web" {
		t.Errorf("opened terminal at %q, want /code/web", opened)
	}

	opened = ""
	m = withRow(t, "MongoDB") // no cwd
	m, _ = m.Update(key("T"))
	if opened != "" {
		t.Error("must not open a terminal with no working directory")
	}
	if mm := m.(model); !mm.flashErr {
		t.Errorf("flash = %q, want an error flash", mm.flash)
	}
}

func TestCopyPickerContextActions(t *testing.T) {
	cases := []struct {
		name string
		row  string
		key  string
		want string
	}{
		{"inspect falls back to lsof for a plain process", "Node service", "i", "lsof -nP -p 101"},
		{"inspect uses brew for a managed service", "MongoDB", "i", "brew services info mongodb-community"},
		{"cd quotes the directory", "Node service", "d", "cd '/code/web'"},
		{"one-liner combines cd and the command", "Node service", "a", "cd '/code/web' && node app.js"},
		{"one-liner without a cwd is just the command", "MongoDB", "a", "/opt/homebrew/opt/mongodb-community/bin/mongod"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var copied string
			orig := copyToClipboard
			copyToClipboard = func(s string) error { copied = s; return nil }
			defer func() { copyToClipboard = orig }()

			m := withRow(t, c.row)
			m, _ = m.Update(key("c"))
			m, _ = m.Update(key(c.key))

			if copied != c.want {
				t.Errorf("copied %q, want %q", copied, c.want)
			}
			if m.(model).copyMenu {
				t.Error("picker should close after a choice")
			}
		})
	}
}

// An avoid row must still get its inspect actions — refusing to STOP something
// is not a reason to refuse to LOOK at it, and "inspect first" is the advice
// the pane itself gives.
func TestAvoidRowStillGetsInspectActions(t *testing.T) {
	var copied string
	orig := copyToClipboard
	copyToClipboard = func(s string) error { copied = s; return nil }
	defer func() { copyToClipboard = orig }()

	m := withRow(t, "macOS service") // StopAvoid
	m, _ = m.Update(key("c"))
	_, _ = m.Update(key("i"))

	if copied != "lsof -nP -p 103" {
		t.Errorf("copied %q, want lsof -nP -p 103", copied)
	}
}

// The pane has to advertise the action, or nobody finds it.
func TestDetailPaneAdvertisesReveal(t *testing.T) {
	m := withRow(t, "Node service")
	out := m.(model).detailView()
	if !strings.Contains(out, "F reveals") {
		t.Errorf("detail pane should hint at the reveal action:\n%s", out)
	}
}

// --- detail-pane field focus ---

// multiPortRow is the case row-level actions could never serve: the table
// shows "44950 +1" and the second port was unreachable.
func multiPortRow() []Row {
	return []Row{{
		L: scan.Listener{PID: 501, Ports: []string{"44950", "44951"}, CommandLine: "/bin/sh serve", Cwd: "/code/api"},
		P: intel.Profile{Identity: "App helper", Source: intel.SrcApp, Risk: intel.High, StopKind: intel.StopAvoid},
	}}
}

func focused(t *testing.T, rows []Row) tea.Model {
	t.Helper()
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: rows, at: time.Now()})
	m, _ = m.Update(key("tab"))
	if !m.(model).paneFocus {
		t.Fatal("tab should move focus into the pane")
	}
	return m
}

func TestTabTogglesPaneFocus(t *testing.T) {
	m := focused(t, sampleRows())
	m, _ = m.Update(key("tab"))
	if m.(model).paneFocus {
		t.Error("tab again should return focus to the table")
	}
	m, _ = m.Update(key("tab"))
	m, _ = m.Update(key("esc"))
	if m.(model).paneFocus {
		t.Error("esc should return focus to the table")
	}
}

// The whole point of per-field focus: open the SECOND port of a multi-port
// row, which the global `o` key can never reach.
func TestPaneFocusOpensSpecificPort(t *testing.T) {
	restore := scan.SetTLSProbeForTesting(func(string) bool { return false })
	defer restore()
	var opened string
	orig := openURL
	openURL = func(u string) error { opened = u; return nil }
	defer func() { openURL = orig }()

	m := focused(t, multiPortRow())
	m, _ = m.Update(key("enter")) // field 0 = first port
	if opened != "http://localhost:44950/" {
		t.Fatalf("first field opened %q, want the first port", opened)
	}

	m, _ = m.Update(key("j")) // field 1 = SECOND port
	_, _ = m.Update(key("enter"))
	if opened != "http://localhost:44951/" {
		t.Errorf("second field opened %q, want http://localhost:44951/", opened)
	}
}

// The other gain: binary and folder are separate targets, where the row-level
// F key has to pick one.
func TestPaneFocusRevealsBinaryAndFolderSeparately(t *testing.T) {
	var revealed []string
	orig := revealPath
	revealPath = func(p string) error { revealed = append(revealed, p); return nil }
	defer func() { revealPath = orig }()

	m := focused(t, multiPortRow())
	// ports 44950, 44951, cmd (/bin/sh exists), dir → indexes 0..3
	for i := 0; i < 2; i++ {
		m, _ = m.Update(key("j"))
	}
	m, _ = m.Update(key("enter")) // cmd
	m, _ = m.Update(key("j"))
	_, _ = m.Update(key("enter")) // dir

	want := []string{"/bin/sh", "/code/api"}
	if len(revealed) != 2 || revealed[0] != want[0] || revealed[1] != want[1] {
		t.Errorf("revealed %v, want %v", revealed, want)
	}
}

func TestPaneFocusStopFieldRespectsAvoid(t *testing.T) {
	var copied string
	orig := copyToClipboard
	copyToClipboard = func(s string) error { copied = s; return nil }
	defer func() { copyToClipboard = orig }()

	// Avoid row: the stop field copies an inspect command, never a kill.
	m := focused(t, multiPortRow())
	for i := 0; i < 4; i++ { // fields: port, port, cmd, dir, stop
		m, _ = m.Update(key("j"))
	}
	m, _ = m.Update(key("enter"))
	if copied != "lsof -nP -p 501" {
		t.Errorf("copied %q, want the inspect command", copied)
	}
	if m.(model).confirm {
		t.Error("an avoid row must never open the stop confirm")
	}

	// A stoppable row opens the confirm instead, pinned like the x key.
	m2 := focused(t, []Row{{
		L: scan.Listener{PID: 502, Ports: []string{"3000"}, CommandLine: "node app.js", Cwd: "/code/web"},
		P: intel.Profile{Identity: "Node", StopKind: intel.StopTerm, StopLabel: "TERM"},
	}})
	for i := 0; i < 2; i++ { // port, dir, stop (cmd is relative, so no field)
		m2, _ = m2.Update(key("j"))
	}
	m2, _ = m2.Update(key("enter"))
	if !m2.(model).confirm || m2.(model).confirmed.L.PID != 502 {
		t.Errorf("stop field should open a confirm pinned to PID 502, got confirm=%v", m2.(model).confirm)
	}
}

func TestPaneCursorWraps(t *testing.T) {
	m := focused(t, multiPortRow()) // 5 fields: 2 ports, cmd, dir, stop
	if got := m.(model).paneIdx; got != 0 {
		t.Fatalf("paneIdx = %d, want 0", got)
	}
	m, _ = m.Update(key("k")) // backwards from the first field
	if got := m.(model).paneIdx; got != 4 {
		t.Errorf("paneIdx after wrapping backwards = %d, want 4", got)
	}
	m, _ = m.Update(key("j"))
	if got := m.(model).paneIdx; got != 0 {
		t.Errorf("paneIdx after wrapping forwards = %d, want 0", got)
	}
	if got := len(paneFields(multiPortRow()[0])); got != 5 {
		t.Errorf("paneFields = %d, want 5 (two ports, cmd, dir, stop)", got)
	}
}

// Every row has at least a stop field (a stoppable row opens the confirm, an
// avoid row copies its inspect command), so Tab always has somewhere to land.
// enterPaneFocus still guards the empty case defensively; this pins the
// invariant that makes the guard unreachable today, so if a future change to
// paneFields breaks it, this fails rather than the pane silently going inert.
func TestEveryRowHasAtLeastOneField(t *testing.T) {
	rows := append(sampleRows(), multiPortRow()...)
	rows = append(rows, Row{ // the barest possible row: no ports, no cwd, no path
		L: scan.Listener{PID: 503, CommandLine: "kernel_task"},
		P: intel.Profile{Identity: "macOS service", StopKind: intel.StopAvoid},
	})
	for _, r := range rows {
		if got := len(paneFields(r)); got == 0 {
			t.Errorf("%s has no actionable pane fields", r.P.Identity)
		}
	}
}

// Focus must not survive onto a different row: field lists differ per row, so
// a stale index would point at the wrong thing.
func TestMovingRowsResetsPaneCursor(t *testing.T) {
	m := focused(t, sampleRows())
	m, _ = m.Update(key("j")) // moves the PANE cursor while focused
	m, _ = m.Update(key("tab"))
	m, _ = m.Update(key("j")) // now moves the TABLE cursor
	if got := m.(model).paneIdx; got != 0 {
		t.Errorf("paneIdx = %d after changing rows, want 0", got)
	}
}

// Non-focus keys must keep working, or focus becomes a mode you get stuck in.
func TestPaneFocusDoesNotSwallowOtherKeys(t *testing.T) {
	m := focused(t, sampleRows())
	m, _ = m.Update(key("c"))
	if !m.(model).copyMenu {
		t.Error("c should still open the copy picker while the pane is focused")
	}
}

// The pane is budgeted exactly detailHeight lines; anything extra pushes the
// whole view past the terminal height and the top scrolls away. Guarding this
// because adding a hint line inside the pane did exactly that.
func TestViewFitsTerminalHeight(t *testing.T) {
	rows := make([]Row, 40)
	for i := range rows {
		rows[i] = Row{
			L: scan.Listener{PID: 100 + i, Ports: []string{"3000"}, CommandLine: "node app.js", Cwd: "/code/web"},
			P: intel.Profile{Identity: "Node", StopKind: intel.StopTerm, Risk: intel.Low, Warning: "a warning line"},
		}
	}
	for _, h := range []int{20, 24, 30, 50} {
		var m tea.Model = New(Options{})
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: h})
		m, _ = m.Update(scannedMsg{rows: rows, at: time.Now()})
		for _, c := range []struct {
			name string
			mm   model
		}{{"table focus", m.(model)}, {"pane focus", m.(model).enterPaneFocus()}} {
			if got := len(strings.Split(c.mm.View(), "\n")); got > h {
				t.Errorf("%s at terminal height %d rendered %d lines", c.name, h, got)
			}
		}
	}
}

// The focus marker must be visible without colour (mono theme, NO_COLOR) and
// must not change the line's geometry — a marker that appears and disappears
// would reflow the pane every time focus moves.
func TestFocusGutterIsConstantWidth(t *testing.T) {
	m := focused(t, multiPortRow())
	unfocusedPane := strings.Split(m.(model).detailView(), "\n")
	mm := m.(model)
	mm.paneFocus = false
	blurredPane := strings.Split(mm.detailView(), "\n")

	if len(unfocusedPane) != len(blurredPane) {
		t.Fatalf("pane changed height on focus: %d vs %d lines", len(unfocusedPane), len(blurredPane))
	}
	for i := range unfocusedPane {
		if a, b := lipgloss.Width(unfocusedPane[i]), lipgloss.Width(blurredPane[i]); a != b {
			t.Errorf("line %d width changed on focus: %d vs %d", i, a, b)
		}
	}
	if !strings.Contains(m.(model).detailView(), "›") {
		t.Error("focused pane should mark the field with a caret, not colour alone")
	}
	if strings.Contains(blurredPane[1], "›") {
		t.Error("unfocused pane must not show a focus caret")
	}
}
