package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

	// an avoid-row must refuse to confirm
	mm := m.(model)
	mm.cursor = 2 // macOS service (StopAvoid)
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
