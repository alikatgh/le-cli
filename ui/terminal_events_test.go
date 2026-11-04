package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func terminalEventModel(t *testing.T) model {
	t.Helper()
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = deliverScanFixture(m, scannedMsg{rows: sampleRows(), at: time.Now()})
	return m.(model)
}

// Terminal modes belong to every rendered view in v2, including the first
// placeholder and overlays, rather than to NewProgram options.
func TestTerminalViewRetainsModesAcrossStates(t *testing.T) {
	states := map[string]model{"starting": New(Options{}), "table": terminalEventModel(t)}
	help := terminalEventModel(t)
	help.help = true
	states["help"] = help
	filter := terminalEventModel(t)
	filter, _ = inspectionUpdate(filter, key("/"))
	states["filter"] = filter
	for name, m := range states {
		t.Run(name, func(t *testing.T) {
			v := m.View()
			if !v.AltScreen || v.MouseMode != tea.MouseModeCellMotion {
				t.Fatalf("terminal modes: alt=%v mouse=%v", v.AltScreen, v.MouseMode)
			}
			if v.Content == "" {
				t.Fatal("view lost its content")
			}
			if v.Cursor != nil || !m.filter.VirtualCursor() {
				t.Fatal("filter must retain its virtual cursor without an extra terminal cursor")
			}
		})
	}
}

func TestKeyReleaseDoesNotRepeatNavigationOrStop(t *testing.T) {
	m := terminalEventModel(t)
	m, _ = inspectionUpdate(m, key("j"))
	cursor := m.cursor
	m, _ = inspectionUpdate(m, tea.KeyReleaseMsg{Code: 'j', Text: "j"})
	if m.cursor != cursor {
		t.Fatal("key release repeated navigation")
	}
	m, cmd := inspectionUpdate(m, tea.KeyReleaseMsg{Code: 'x', Text: "x"})
	if m.confirm || cmd != nil {
		t.Fatal("key release opened a stop confirmation")
	}
	_, cmd = inspectionUpdate(m, key("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c must still quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c returned a non-quit command")
	}
}

func TestMouseMotionAndReleaseCannotRetargetContextMenu(t *testing.T) {
	m := terminalEventModel(t)
	m, _ = inspectionUpdate(m, tea.MouseClickMsg{Button: tea.MouseRight, Y: 2})
	if !m.actionMenu || m.actionRow.L.PID != 101 {
		t.Fatal("right click must pin the clicked row")
	}
	for _, msg := range []tea.Msg{
		tea.MouseMotionMsg{Button: tea.MouseRight, Y: 3},
		tea.MouseReleaseMsg{Button: tea.MouseRight, Y: 3},
		tea.MouseMotionMsg{Button: tea.MouseLeft, Y: 4},
		tea.MouseReleaseMsg{Button: tea.MouseLeft, Y: 4},
	} {
		m, _ = inspectionUpdate(m, msg)
		if !m.actionMenu || m.actionRow.L.PID != 101 || m.cursor != 0 {
			t.Fatalf("%T changed the selected/pinned row", msg)
		}
	}
	m, _ = inspectionUpdate(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.cursor != 1 || m.actionRow.L.PID != 101 {
		t.Fatal("wheel must navigate without retargeting the pinned menu")
	}
	m, _ = inspectionUpdate(m, tea.MouseClickMsg{Button: tea.MouseLeft, Y: 4})
	if m.actionMenu || m.cursor != 2 {
		t.Fatal("left click must dismiss the menu and select its row")
	}
}

func TestPasteUpdatesFilterWithoutInvokingShortcuts(t *testing.T) {
	m := terminalEventModel(t)
	m, cmd := inspectionUpdate(m, tea.PasteMsg{Content: "px"})
	if m.paused || m.confirm || cmd != nil || m.filter.Value() != "" {
		t.Fatal("pasted shortcut characters must be inert outside filter input")
	}
	m, _ = inspectionUpdate(m, key("/"))
	m, _ = inspectionUpdate(m, tea.PasteMsg{Content: "Mongo"})
	if m.filter.Value() != "Mongo" || len(m.view) != 1 || m.view[0].L.PID != 102 {
		t.Fatal("paste must update both the text and visible rows immediately")
	}
	m, _ = inspectionUpdate(m, key("esc"))
	if m.filtering || m.filter.Value() != "" || len(m.view) != 3 {
		t.Fatal("escape must clear the pasted filter and restore all rows")
	}
}

func TestSpaceKeyFoldsSelectedGroup(t *testing.T) {
	m := grouped(t, machineRows()).(model)
	m.cursor = -1
	for i, item := range m.items {
		if item.header {
			m.cursor = i
			break
		}
	}
	owner, ok := m.selectedGroup()
	if !ok {
		t.Fatal("missing group header")
	}
	beforeHidden := m.items[m.cursor].hidden
	beforeVisible := len(visibleRows(m))
	if beforeHidden == 0 {
		t.Fatal("fixture must begin on a collapsed group")
	}
	m, _ = inspectionUpdate(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if selected, ok := m.selectedGroup(); !ok || selected != owner || m.items[m.cursor].hidden != 0 {
		t.Fatal("space must expand and retain the selected group")
	}
	if len(visibleRows(m)) != beforeVisible+beforeHidden {
		t.Fatal("expanding the group must reveal all its listeners")
	}
	m, _ = inspectionUpdate(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if selected, ok := m.selectedGroup(); !ok || selected != owner || m.items[m.cursor].hidden != beforeHidden {
		t.Fatal("a second space must collapse the same selected group")
	}
	if len(visibleRows(m)) != beforeVisible {
		t.Fatal("collapsing the group must restore the original visible listener count")
	}
}

// Normal exit must not clear the keyboard state Bubble Tea has just restored
// from its parent; emergency recovery is the only path that resets it in place.
func TestNormalCleanupPreservesRestoredKeyboardProtocol(t *testing.T) {
	for _, sequence := range []string{"\x1b[>4m", "\x1b[=0;1u", "\x1b[<"} {
		if strings.Contains(resetTerminalModes, sequence) {
			t.Fatalf("normal cleanup must not alter the restored keyboard protocol: %q", sequence)
		}
	}
	if !strings.HasSuffix(ResetTerminal, resetTerminalModes) {
		t.Fatal("emergency recovery must reset keyboard modes before leaving the active screen")
	}
}
