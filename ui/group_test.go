package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// machineRows mirrors a real listing: 15 listeners of which 12 are background
// helpers le refuses to stop. This shape is the whole reason grouping exists.
func machineRows() []Row {
	mk := func(pid int, port, id string, src intel.Source, risk intel.Risk, sk intel.StopKind, label string) Row {
		return Row{
			L: scan.Listener{PID: pid, Ports: []string{port}, Cwd: "/"},
			P: intel.Profile{Identity: id, Source: src, Risk: risk, StopKind: sk, StopLabel: label},
		}
	}
	return []Row{
		mk(3580, "3354", "Pulse Secure", intel.SrcApp, intel.High, intel.StopAvoid, ""),
		mk(765, "5000", "ControlCenter", intel.SrcMacOS, intel.High, intel.StopAvoid, ""),
		mk(69148, "5037", "adb", intel.SrcTerminal, intel.Low, intel.StopTerm, "Send TERM to PID 69148"),
		mk(64597, "5055", "app.py", intel.SrcTerminal, intel.Low, intel.StopTerm, "Send TERM to PID 64597"),
		mk(22162, "5555", "BlueStacks", intel.SrcApp, intel.High, intel.StopAvoid, ""),
		mk(1809, "15292", "Adobe", intel.SrcApp, intel.High, intel.StopAvoid, ""),
		mk(92035, "42050", "OneDrive", intel.SrcApp, intel.High, intel.StopAvoid, ""),
		mk(1087, "44950", "Figma", intel.SrcApp, intel.High, intel.StopAvoid, ""),
		mk(27470, "50010", "企业微信", intel.SrcApp, intel.High, intel.StopAvoid, ""),
		mk(68564, "51484", "Grammarly Desktop", intel.SrcApp, intel.High, intel.StopAvoid, ""),
		mk(80074, "55387", "Antigravity IDE", intel.SrcIDE, intel.Med, intel.StopAvoid, ""),
		mk(80195, "55396", "Antigravity IDE", intel.SrcIDE, intel.Med, intel.StopAvoid, ""),
		mk(80730, "55535", "Antigravity IDE", intel.SrcIDE, intel.Med, intel.StopAvoid, ""),
		mk(80690, "55534", "Node service", intel.SrcTerminal, intel.Low, intel.StopTerm, "Send TERM to PID 80690"),
		mk(727, "55716", "macOS service", intel.SrcMacOS, intel.High, intel.StopAvoid, ""),
	}
}

func grouped(t *testing.T, rows []Row) tea.Model {
	t.Helper()
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 128, Height: 40})
	m, _ = m.Update(scannedMsg{rows: rows, at: time.Now()})
	m, _ = m.Update(key("z"))
	if !m.(model).grouped {
		t.Fatal("z should turn grouping on")
	}
	return m
}

func visibleRows(mm model) []Row {
	var out []Row
	for _, it := range mm.items {
		if !it.header {
			out = append(out, it.row)
		}
	}
	return out
}

// Grouping must shorten the list by folding what you cannot act on, and must
// never fold away a listener you can.
func TestGroupingFoldsOnlyTheUnactionable(t *testing.T) {
	flat := New(Options{})
	flat.all = machineRows()
	flat.applyFilter()
	if got := len(flat.items); got != 15 {
		t.Fatalf("flat list = %d lines, want 15", got)
	}

	m := grouped(t, machineRows())
	mm := m.(model)
	if len(mm.items) >= 15 {
		t.Errorf("grouped list = %d lines, want fewer than the flat 15", len(mm.items))
	}
	for _, r := range visibleRows(mm) {
		_ = r
	}
	// Every stoppable listener is still on screen.
	for _, want := range []string{"adb", "app.py", "Node service"} {
		found := false
		for _, r := range visibleRows(mm) {
			if r.P.Identity == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q was folded away — grouping must never hide a row you can act on", want)
		}
	}
	// …and the eight app helpers are folded behind one header.
	for _, r := range visibleRows(mm) {
		if r.P.Identity == "OneDrive" {
			t.Error("the all-refused app group should start folded")
		}
	}
}

// Folding hides rows, so the header has to say what is inside — otherwise
// "which port is that?" needs an unfold first.
func TestFoldedGroupHeaderListsItsPorts(t *testing.T) {
	m := grouped(t, machineRows())
	out := m.(model).tableView()
	for _, port := range []string{"42050", "50010"} { // OneDrive, 企业微信
		if !strings.Contains(out, port) {
			t.Errorf("folded group header should still list port %s:\n%s", port, out)
		}
	}
}

// A small group is not worth a header: folding two rows behind one line saves
// one line and costs two ports of visibility.
func TestSmallGroupsStayOpen(t *testing.T) {
	two := machineRows()[:1]
	two = append(two, Row{L: scan.Listener{PID: 9, Ports: []string{"9999"}},
		P: intel.Profile{Identity: "Other", Source: intel.SrcApp, Risk: intel.High, StopKind: intel.StopAvoid}})
	if defaultCollapsed(two) {
		t.Error("a 2-row group should stay open")
	}
}

// A filter is a search. Folding a match would make the search lie.
func TestFilterExpandsEveryGroup(t *testing.T) {
	m := grouped(t, machineRows())
	m, _ = m.Update(key("/"))
	for _, r := range "onedrive" {
		m, _ = m.Update(key(string(r)))
	}
	mm := m.(model)
	found := false
	for _, r := range visibleRows(mm) {
		if r.P.Identity == "OneDrive" {
			found = true
		}
	}
	if !found {
		t.Error("a filtered match must be visible even though its group is normally folded")
	}
}

// A pinned port is one the user explicitly asked to keep in sight.
func TestPinnedPortKeepsItsGroupOpen(t *testing.T) {
	m := grouped(t, machineRows())
	mm := m.(model)
	mm.favs = map[string]bool{"42050": true} // OneDrive, inside the folded app group
	mm.applyFilter()
	found := false
	for _, r := range visibleRows(mm) {
		if r.P.Identity == "OneDrive" {
			found = true
		}
	}
	if !found {
		t.Error("a group containing a pinned port must not fold")
	}
}

// enter toggles the group under the cursor, and only when the cursor is on a
// header — on a row it must do nothing rather than fold the row's group.
func TestEnterTogglesOnlyOnAHeader(t *testing.T) {
	m := grouped(t, machineRows())
	before := len(m.(model).items)

	m, _ = m.Update(key("enter")) // cursor 0 is the folded app header
	after := len(m.(model).items)
	if after <= before {
		t.Fatalf("enter on a folded header should expand it: %d -> %d lines", before, after)
	}

	// Move onto a row inside the now-open group and press enter again.
	m, _ = m.Update(key("j"))
	if _, isHeader := m.(model).selectedGroup(); isHeader {
		t.Fatal("expected a row under the cursor")
	}
	m, _ = m.Update(key("enter"))
	if got := len(m.(model).items); got != after {
		t.Errorf("enter on a row changed the list: %d -> %d", after, got)
	}
}

// A header is a line, not a listener. Row actions must refuse on it rather
// than silently acting on a neighbouring row.
func TestRowActionsRefuseOnAGroupHeader(t *testing.T) {
	m := grouped(t, machineRows())
	if _, ok := m.(model).selected(); ok {
		t.Fatal("a group header must not report a selected listener")
	}
	var copied string
	orig := copyToClipboard
	copyToClipboard = func(s string) error { copied = s; return nil }
	defer func() { copyToClipboard = orig }()

	m, _ = m.Update(key("c")) // copy picker
	if m.(model).copyMenu {
		t.Error("the copy picker should not open on a group header")
	}
	m, _ = m.Update(key("x")) // stop
	if m.(model).confirm {
		t.Error("the stop confirm should not open on a group header")
	}
	if copied != "" {
		t.Errorf("nothing should have been copied, got %q", copied)
	}
}

// Z folds and unfolds everything with one key.
func TestShiftZFoldsAndUnfoldsAll(t *testing.T) {
	m := grouped(t, machineRows())
	m, _ = m.Update(key("Z")) // some groups were open, so this folds all
	mm := m.(model)
	if len(visibleRows(mm)) != 0 {
		t.Errorf("Z should fold every group, %d rows still visible", len(visibleRows(mm)))
	}
	m, _ = m.Update(key("Z"))
	if got := len(visibleRows(m.(model))); got != 15 {
		t.Errorf("Z again should unfold every group, got %d rows", got)
	}
}

// Toggling must never leave the cursor past the end of the list.
func TestCursorStaysValidWhenFolding(t *testing.T) {
	m := grouped(t, machineRows())
	m, _ = m.Update(key("G")) // jump to the last line
	m, _ = m.Update(key("Z")) // fold everything: the list gets much shorter
	mm := m.(model)
	if mm.cursor >= len(mm.items) {
		t.Errorf("cursor %d is past the end of a %d-line list", mm.cursor, len(mm.items))
	}
	if _, ok := mm.selected(); ok {
		// fine either way — just must not panic or point at nothing
		_ = ok
	}
	if v := mm.View(); v == "" {
		t.Error("view should still render after folding")
	}
}

// Turning grouping off restores exactly the flat list.
func TestZTogglesBackToFlat(t *testing.T) {
	m := grouped(t, machineRows())
	m, _ = m.Update(key("z"))
	mm := m.(model)
	if mm.grouped {
		t.Fatal("z should turn grouping off")
	}
	if got := len(mm.items); got != 15 {
		t.Errorf("flat list = %d lines, want 15", got)
	}
	for _, it := range mm.items {
		if it.header {
			t.Error("a flat list must contain no group headers")
		}
	}
}

// The config can make grouping the default without a keypress.
func TestGroupOptionStartsGrouped(t *testing.T) {
	var m tea.Model = New(Options{Group: true})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 128, Height: 40})
	m, _ = m.Update(scannedMsg{rows: machineRows(), at: time.Now()})
	if !m.(model).grouped {
		t.Error("Options.Group should start the TUI grouped")
	}
}

// Pane focus cannot survive the cursor landing on a group header — a header
// has no fields, and a footer advertising actions for a row that isn't there
// is worse than no focus at all. While focused, j/k move between FIELDS, so
// the cursor can't walk onto a header; the way it happens is a rebuild that
// changes what line the cursor index refers to (toggling grouping, a group
// folding, a background scan).
func TestPaneFocusDropsWhenARebuildLandsOnAHeader(t *testing.T) {
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 128, Height: 40})
	m, _ = m.Update(scannedMsg{rows: machineRows(), at: time.Now()})
	m, _ = m.Update(key("tab")) // focus the pane on the first row, flat
	if !m.(model).paneFocus {
		t.Fatal("expected pane focus on a row")
	}

	m, _ = m.Update(key("z")) // group: line 0 is now a header, not a row
	mm := m.(model)
	if _, ok := mm.selected(); ok {
		t.Fatal("expected a group header under the cursor after grouping")
	}
	if mm.paneFocus {
		t.Error("pane focus should drop when the cursor no longer sits on a row")
	}
	if v := mm.View(); v == "" {
		t.Error("view should still render")
	}
}
