package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// Field-level focus in the detail pane — the TUI answer to the mac app's
// clickable per-field actions. Without it the pane is a read-only reflection
// of the selected row and every action has to be a global key acting on "the
// row", which forces two compromises the app doesn't make:
//
//   - only ONE reveal target per row (folder or binary, never both), and
//   - no way to act on a row's SECOND port — the "+1"/"+2" extras in the
//     table were unreachable.
//
// Tab moves focus in, j/k step between fields, Enter runs the focused field's
// action, Tab/esc returns to the table.

type fieldKind int

const (
	fieldPort fieldKind = iota
	fieldCmd
	fieldDir
	fieldStop
)

// paneField is one actionable value in the detail pane.
type paneField struct {
	kind fieldKind
	arg  string // the port, for fieldPort
	hint string // what Enter will do, shown on the focused line
}

// paneFields lists what can be acted on for this row, in pane reading order.
// A field only appears when its action can actually run — an empty list means
// Tab does nothing, which is honest for a row with no ports, no resolvable
// binary and no working directory.
func paneFields(r Row) []paneField {
	var fs []paneField
	for _, p := range r.L.Ports {
		fs = append(fs, paneField{kind: fieldPort, arg: p, hint: "⏎ opens :" + p})
	}
	if exe := executablePath(r.L.CommandLine); exe != "" {
		fs = append(fs, paneField{kind: fieldCmd, hint: "⏎ reveals the binary"})
	}
	if r.L.Cwd != "" {
		fs = append(fs, paneField{kind: fieldDir, hint: "⏎ reveals the folder"})
	}
	// The stop line is always actionable: a stoppable row opens the confirm,
	// an avoid row copies its inspect command — which is the action its own
	// "inspect first" advice asks for.
	if r.P.StopKind == intel.StopAvoid {
		fs = append(fs, paneField{kind: fieldStop, hint: "⏎ copies " + inspectCommand(r)})
	} else {
		fs = append(fs, paneField{kind: fieldStop, hint: "⏎ stops it (asks first)"})
	}
	return fs
}

// focusedField returns the field the pane cursor is on, if focus is in the pane.
func (m model) focusedField() (paneField, bool) {
	if !m.paneFocus {
		return paneField{}, false
	}
	r, ok := m.selected()
	if !ok {
		return paneField{}, false
	}
	fs := paneFields(r)
	if m.paneIdx < 0 || m.paneIdx >= len(fs) {
		return paneField{}, false
	}
	return fs[m.paneIdx], true
}

// enterPaneFocus moves focus into the pane. It refuses when the row has
// nothing actionable rather than trapping the cursor in an inert pane.
func (m model) enterPaneFocus() model {
	r, ok := m.selected()
	if !ok {
		return m
	}
	if len(paneFields(r)) == 0 {
		m.flash, m.flashErr = "nothing to act on in "+r.P.Identity, true
		return m
	}
	m.paneFocus, m.paneIdx, m.flash = true, 0, ""
	return m
}

// movePaneCursor steps through the fields, wrapping — a short list is faster
// to cycle than to clamp against.
func (m model) movePaneCursor(delta int) model {
	r, ok := m.selected()
	if !ok {
		return m
	}
	n := len(paneFields(r))
	if n == 0 {
		m.paneFocus = false
		return m
	}
	m.paneIdx = ((m.paneIdx+delta)%n + n) % n
	return m
}

// runPaneField performs the focused field's action. Returns the model and any
// command to run (opening a browser is synchronous here, same as the `o` key).
func (m model) runPaneField() model {
	r, ok := m.selected()
	if !ok {
		return m
	}
	f, ok := m.focusedField()
	if !ok {
		return m
	}
	switch f.kind {
	case fieldPort:
		// The point of per-port focus: a row listening on 44950 AND 44951 can
		// finally open the second one. The `o` key only ever opens the first.
		url := scan.Scheme(f.arg) + "://localhost:" + f.arg + "/"
		if err := openURL(url); err != nil {
			m.flash, m.flashErr = "couldn't open browser: "+err.Error(), true
		} else {
			m.flash, m.flashErr = "opened "+url, false
		}
	case fieldCmd:
		exe := executablePath(r.L.CommandLine)
		if exe == "" {
			m.flash, m.flashErr = "can't resolve the binary for "+r.P.Identity, true
		} else if err := revealPath(exe); err != nil {
			m.flash, m.flashErr = "couldn't reveal "+exe, true
		} else {
			m.flash, m.flashErr = "revealed "+exe, false
		}
	case fieldDir:
		if err := revealPath(r.L.Cwd); err != nil {
			m.flash, m.flashErr = "couldn't reveal "+r.L.Cwd, true
		} else {
			m.flash, m.flashErr = "revealed "+r.L.Cwd, false
		}
	case fieldStop:
		if r.P.StopKind == intel.StopAvoid {
			m.copyResult(inspectCommand(r))
		} else {
			// Pin the row exactly as the x key does, so a background scan
			// can't retarget the pending stop. (LE-406)
			m.confirm, m.confirmed = true, r
		}
	}
	return m
}

// paneLabel renders a pane line's label, highlighted when that line holds the
// focused field. The highlight is a style change only — no reflow, no shifted
// columns — so focus never moves the text under the reader.
func (m model) paneLabel(label string, kind fieldKind) string {
	f, ok := m.focusedField()
	if ok && f.kind == kind {
		return lipgloss.NewStyle().Foreground(brand).Bold(true).Render(label)
	}
	return dimSt.Render(label)
}

// portsCell renders the ports list with the focused port highlighted in place.
func (m model) portsCell(r Row) string {
	f, focused := m.focusedField()
	parts := make([]string, 0, len(r.L.Ports))
	for _, p := range r.L.Ports {
		if focused && f.kind == fieldPort && f.arg == p {
			parts = append(parts, lipgloss.NewStyle().Foreground(brand).Bold(true).Render(p))
			continue
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// paneHint is the one-line "what Enter does" footer inside the pane.
func (m model) paneHint() string {
	f, ok := m.focusedField()
	if !ok {
		return dimSt.Render("tab  act on a field")
	}
	return keySt.Render("j/k") + dimSt.Render(" field  ") +
		lipgloss.NewStyle().Foreground(brand).Render(f.hint) +
		dimSt.Render("   tab/esc back")
}

// gutter renders the 2-column focus marker for a pane line. Pass a kind of -1
// for lines that hold no field. The width is constant in both states — a
// marker that appeared and disappeared would reflow the line under the reader
// every time focus moved.
func (m model) gutter(kind fieldKind) string {
	f, ok := m.focusedField()
	if ok && kind >= 0 && f.kind == kind {
		return lipgloss.NewStyle().Foreground(brand).Bold(true).Render("› ")
	}
	return "  "
}
