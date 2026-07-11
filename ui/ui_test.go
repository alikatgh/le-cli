package ui

import (
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
	m, _ = m.Update(key("o"))

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
