package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// Legacy view fixtures represent completed scans without executing commands.
// Start a real request in the reducer so these fixtures exercise ID matching,
// rather than granting untagged production messages a special bypass.
func deliverScanFixture(m tea.Model, result scannedMsg) (tea.Model, tea.Cmd) {
	mm := m.(model)
	if !mm.scanInFlight {
		mm.requestScan(true)
	}
	result.id = mm.activeScanID
	return mm.Update(result)
}

func inspectionUpdate(m model, msg tea.Msg) (model, tea.Cmd) {
	next, cmd := m.update(msg)
	return next.(model), cmd
}

func inspectionModel(t *testing.T) model {
	t.Helper()
	stubFavoritesDir(t)
	m := New(Options{})
	m.w, m.h = 120, 40
	return m
}

func inspectionRow(pid int, start, command, port string) Row {
	l := scan.Listener{PID: pid, StartTime: start, Command: "node", CommandLine: command, Ports: []string{port}}
	return Row{L: l, P: intel.Make(l, intel.Env{})}
}

func TestInspectionPauseKeepsRunningSlotAndManualRefresh(t *testing.T) {
	m := inspectionModel(t)
	m, _ = inspectionUpdate(m, scanRequestedMsg{})
	first := m.activeScanID
	at := time.Now().Add(-time.Second)
	original := inspectionRow(1, "original", "node original.js", "3000")
	m, _ = inspectionUpdate(m, scannedMsg{id: first, rows: []Row{original}, at: at})
	m, _ = inspectionUpdate(m, tickMsg(time.Now()))
	running := m.activeScanID
	m, _ = inspectionUpdate(m, key("p"))
	if !m.paused || !m.scanInFlight {
		t.Fatal("pause must retain the active command slot")
	}
	for i := 0; i < 10; i++ {
		m, _ = inspectionUpdate(m, tickMsg(time.Now()))
	}
	if m.pendingScan {
		t.Fatal("paused ticks must not queue work")
	}
	m, cmd := inspectionUpdate(m, key("r"))
	if cmd != nil || !m.pendingManual || m.activeScanID != running {
		t.Fatal("manual refresh must coalesce behind the running scan")
	}
	m, cmd = inspectionUpdate(m, scannedMsg{id: running, rows: []Row{inspectionRow(2, "new", "node new.js", "3001")}, at: time.Now()})
	if cmd == nil || !m.scanInFlight || !m.paused || m.activeScanID == running {
		t.Fatal("one paused manual successor was not started")
	}
	if m.all[0].L.PID != 1 || !m.lastScan.Equal(at) {
		t.Fatal("in-flight live result changed the paused snapshot")
	}
	manual := m.activeScanID
	m, _ = inspectionUpdate(m, scannedMsg{id: manual, rows: []Row{inspectionRow(3, "manual", "node once.js", "3002")}, at: time.Now()})
	if !m.paused || m.scanInFlight || m.all[0].L.PID != 3 {
		t.Fatal("manual refresh must update once and remain paused")
	}
	header := m.headerView()
	if !strings.Contains(header, "paused") || !strings.Contains(header, "snapshot") || !strings.Contains(header, "old") {
		t.Fatalf("missing pause/age feedback: %s", header)
	}
	m, cmd = inspectionUpdate(m, key("p"))
	if m.paused || cmd == nil || !m.scanInFlight {
		t.Fatal("resume must immediately request a live snapshot")
	}
}

func TestInspectionCoalescesAndRejectsMismatchedResults(t *testing.T) {
	m := inspectionModel(t)
	m, _ = inspectionUpdate(m, scanRequestedMsg{})
	active := m.activeScanID
	for i := 0; i < 12; i++ {
		m, _ = inspectionUpdate(m, tickMsg(time.Now()))
		m, _ = inspectionUpdate(m, key("r"))
		m, _ = inspectionUpdate(m, stopResultMsg{ok: "stopped"})
	}
	if m.activeScanID != active || m.nextScanID != 1 || !m.pendingScan {
		t.Fatal("requests launched concurrent scans")
	}
	beforeFlash := m.flash
	for _, id := range []uint64{0, active + 1} {
		m, _ = inspectionUpdate(m, scannedMsg{id: id, err: errors.New("unrelated"), at: time.Now()})
		if !m.scanInFlight || m.activeScanID != active || m.flash != beforeFlash {
			t.Fatal("mismatched error released slot or changed display")
		}
	}
	good := inspectionRow(1, "start", "node server.js", "3000")
	at := time.Now()
	m, cmd := inspectionUpdate(m, scannedMsg{id: active, rows: []Row{good}, at: at})
	if cmd == nil || !m.scanInFlight || m.nextScanID != 2 || m.pendingScan {
		t.Fatal("requests did not collapse into exactly one successor")
	}
	successor := m.activeScanID
	m, _ = inspectionUpdate(m, scannedMsg{id: active, rows: nil, err: errors.New("stale"), at: at.Add(time.Second)})
	if !m.scanInFlight || m.activeScanID != successor || len(m.all) != 1 || m.flash != beforeFlash {
		t.Fatal("stale completion affected successor")
	}
	m, cmd = inspectionUpdate(m, scannedMsg{id: successor, rows: nil, err: errors.New("denied"), at: at.Add(time.Second)})
	if cmd != nil || m.scanInFlight || len(m.all) != 1 || !m.lastScan.Equal(at) || !strings.Contains(m.flash, "denied") {
		t.Fatal("failed scan did not preserve last good snapshot")
	}
}

func TestInspectionPauseResumeBeforeCompletionRejectsOldSnapshot(t *testing.T) {
	m := inspectionModel(t)
	m, _ = inspectionUpdate(m, scanRequestedMsg{})
	old := m.activeScanID
	m, _ = inspectionUpdate(m, key("p"))
	m, cmd := inspectionUpdate(m, key("p"))
	if cmd != nil || !m.pendingScan || !m.scanInFlight {
		t.Fatal("resume must wait for active command")
	}
	m, cmd = inspectionUpdate(m, scannedMsg{id: old, rows: []Row{inspectionRow(1, "old", "node old.js", "3000")}, at: time.Now()})
	if len(m.all) != 0 || cmd == nil || m.activeScanID == old {
		t.Fatal("pause-invalidated result applied after resuming")
	}
}

func TestInspectionPauseKeyRespectsModes(t *testing.T) {
	for _, mode := range []string{"filter", "copy", "confirm", "action"} {
		t.Run(mode, func(t *testing.T) {
			m := inspectionModel(t)
			row := inspectionRow(1, "start", "node server.js", "3000")
			switch mode {
			case "filter":
				m.filtering = true
				m.filter.Focus()
			case "copy":
				m.copyMenu = true
				m.copyRow = row
			case "confirm":
				m.confirm = true
				m.confirmed = row
			case "action":
				m.actionMenu = true
				m.actionRow = row
			}
			oldCopy := copyToClipboard
			copyToClipboard = func(string) error { return nil }
			defer func() { copyToClipboard = oldCopy }()
			m, _ = inspectionUpdate(m, key("p"))
			if m.paused {
				t.Fatal("p stole a modal key")
			}
			if mode == "filter" && m.filter.Value() != "p" {
				t.Fatal("p was not typed into filter")
			}
			if mode == "copy" && m.copyMenu {
				t.Fatal("copy ps action did not run")
			}
		})
	}
}

func TestInspectionSelectionSurvivesRefreshAndRenderedSort(t *testing.T) {
	m := inspectionModel(t)
	a := inspectionRow(1, "start-a", "node a.js", "9000")
	b := inspectionRow(2, "start-b", "node b.js", "3000")
	m.requestScan(true)
	m, _ = inspectionUpdate(m, scannedMsg{id: m.activeScanID, rows: []Row{a, b}, at: time.Now()})
	m.cursor = 1 // a under port sort
	m, _ = inspectionUpdate(m, key("2"))
	selected, ok := m.selected()
	if !ok || selected.L.PID != 1 || m.items[0].row.L.PID != 1 || m.items[1].row.L.PID != 2 {
		t.Fatal("rendered/actions order did not follow PID sort while retaining identity")
	}
	m.requestScan(true)
	a.L.CPU = 99
	m, _ = inspectionUpdate(m, scannedMsg{id: m.activeScanID, rows: []Row{b, a}, at: time.Now()})
	selected, ok = m.selected()
	if !ok || selected.L.PID != 1 || selected.L.CPU != 99 {
		t.Fatal("refresh lost identity or kept stale rendered row")
	}
	// PID reuse must clear action selection until explicit navigation.
	a.L.StartTime = "replacement"
	m.requestScan(true)
	m, _ = inspectionUpdate(m, scannedMsg{id: m.activeScanID, rows: []Row{a, b}, at: time.Now()})
	if _, ok = m.selected(); ok || !m.selectionLost {
		t.Fatal("recycled PID silently inherited selection")
	}
	m, _ = inspectionUpdate(m, key("/"))
	m, _ = inspectionUpdate(m, key("n"))
	m, _ = inspectionUpdate(m, key("esc"))
	if _, ok = m.selected(); ok || !m.selectionLost {
		t.Fatal("filter/cancel silently reselected replacement")
	}
	m, _ = inspectionUpdate(m, key("x"))
	if m.confirm {
		t.Fatal("replacement process could be stopped without explicit selection")
	}
	m, _ = inspectionUpdate(m, key("g"))
	if _, ok = m.selected(); !ok {
		t.Fatal("explicit navigation did not restore selection")
	}
}

func TestInspectionFallbackSelectionRequiresFullUnchangedCommand(t *testing.T) {
	base := scan.Listener{PID: 1, Command: "node", CommandLine: "node app.js"}
	for _, tc := range []struct {
		name string
		a, b scan.Listener
		want bool
	}{
		{"full argv", base, base, true},
		{"changed argv", base, scan.Listener{PID: 1, Command: "node", CommandLine: "node other.js"}, false},
		{"name fallback", scan.Listener{PID: 1, Command: "node", CommandLine: "node"}, scan.Listener{PID: 1, Command: "node", CommandLine: "node"}, false},
		{"executable fallback", scan.Listener{PID: 1, Command: "node.exe", CommandLine: `C:\node\node.exe`, CommandLineFallback: true}, scan.Listener{PID: 1, Command: "node.exe", CommandLine: `C:\node\node.exe`, CommandLineFallback: true}, false},
		{"start unavailable", base, scan.Listener{PID: 1, Command: "node", CommandLine: "node app.js", StartTime: "now"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameProcess(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameProcess=%v want %v", got, tc.want)
			}
		})
	}
}

func TestInspectionKeepsGroupHeaderOnRefreshAndSort(t *testing.T) {
	m := inspectionModel(t)
	m.grouped = true
	a := inspectionRow(1, "a", "node a.js", "9000")
	b := inspectionRow(2, "b", "node b.js", "3000")
	b.P.Source = intel.SrcApp
	m.requestScan(true)
	m, _ = inspectionUpdate(m, scannedMsg{id: m.activeScanID, rows: []Row{a, b}, at: time.Now()})
	owner, _ := m.selectedGroup()
	m, _ = inspectionUpdate(m, key("2"))
	if got, ok := m.selectedGroup(); !ok || got != owner {
		t.Fatal("sort changed selected group")
	}
	m.requestScan(true)
	m, _ = inspectionUpdate(m, scannedMsg{id: m.activeScanID, rows: []Row{b, a}, at: time.Now()})
	if got, ok := m.selectedGroup(); !ok || got != owner {
		t.Fatal("refresh changed selected group")
	}
	if _, ok := m.selected(); ok {
		t.Fatal("group header became actionable row")
	}
}

func TestInspectionEvidenceAndLongPaneAreBounded(t *testing.T) {
	m := inspectionModel(t)
	m.w = 100
	row := inspectionRow(1, "start", "node "+strings.Repeat("very-long-command", 60), "3000")
	row.P.Identity = strings.Repeat("process", 100)
	row.P.Restart = strings.Repeat("restart ", 100)
	row.P.Warning = strings.Repeat("warning ", 100)
	m.all = []Row{row}
	m.applyFilter()
	view := m.detailView()
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > m.w {
			t.Errorf("pane width %d exceeds %d", width, m.w)
		}
	}
	for _, want := range []string{"listener observed", "cmd observed", "dir unavailable", "start observed", "class inferred", "owner inferred", "stop", "back"} {
		if !strings.Contains(view, want) {
			t.Errorf("essential inspection field %q missing from bounded pane", want)
		}
	}
	if strings.Contains(view, "% sure") {
		t.Fatal("heuristic score rendered as measured probability")
	}
}
