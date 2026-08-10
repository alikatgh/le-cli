package ui

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// Three cursor mechanics landed in one day — pane focus, grouping with folded
// headers, and content-sized columns — and they all touch the same state. Unit
// tests cover each in isolation; this drives them together.
//
// Every key is fed to the model in sequences a person could actually type, and
// after each one the model must still satisfy the things the renderer assumes.
// A violated invariant here is a panic or a wrong-row action in the real TUI.
func checkInvariants(t *testing.T, mm model, trail []string) {
	t.Helper()
	checkStateInvariants(t, mm, trail)
	if v := mm.View(); v == "" {
		t.Fatalf("empty view — after keys: %s", strings.Join(trail, " "))
	}
}

// The state half, split out so the degenerate-size sweep can assert all of it
// on a zero-area window, where an empty view is legitimate rather than a bug.
func checkStateInvariants(t *testing.T, mm model, trail []string) {
	t.Helper()
	ctx := func() string { return "after keys: " + strings.Join(trail, " ") }

	if len(mm.items) > 0 {
		if mm.cursor < 0 || mm.cursor >= len(mm.items) {
			t.Fatalf("cursor %d out of range for %d items — %s", mm.cursor, len(mm.items), ctx())
		}
	}
	if mm.offset < 0 {
		t.Fatalf("negative offset %d — %s", mm.offset, ctx())
	}
	if len(mm.items) > 0 && mm.offset >= len(mm.items) {
		t.Fatalf("offset %d past %d items — %s", mm.offset, len(mm.items), ctx())
	}
	// selected() is what every row action trusts; it must agree with the line
	// the cursor is actually on.
	sel, ok := mm.selected()
	if len(mm.items) > 0 {
		it := mm.items[mm.cursor]
		if ok == it.header {
			t.Fatalf("selected()=%v but items[%d].header=%v — %s", ok, mm.cursor, it.header, ctx())
		}
		if ok && sel.L.PID != it.row.L.PID {
			t.Fatalf("selected() returned pid %d, cursor is on pid %d — %s", sel.L.PID, it.row.L.PID, ctx())
		}
	}
	// Pane focus is only meaningful on a row with fields.
	if mm.paneFocus {
		if _, isRow := mm.selected(); !isRow {
			t.Fatalf("pane focus with no row selected — %s", ctx())
		}
	}
	// Labels are indexed by viewIdx; an out-of-range index would panic the
	// renderer rather than merely mislabel.
	for _, it := range mm.items {
		if !it.header && (it.viewIdx < 0 || it.viewIdx >= len(mm.view)) {
			t.Fatalf("item viewIdx %d out of range for %d view rows — %s", it.viewIdx, len(mm.view), ctx())
		}
	}
}

// varietyRows deliberately mixes everything that has broken a layout: CJK,
// multi-port cells, colliding identities, refused and stoppable rows, missing
// cwd, several owners.
func varietyRows(n int) []Row {
	owners := []intel.Source{intel.SrcApp, intel.SrcMacOS, intel.SrcIDE, intel.SrcTerminal, intel.SrcHomebrew}
	names := []string{"OneDrive", "企业微信", "Antigravity IDE", "Node service", "Grammarly Desktop", "adb"}
	helpers := []string{"Electron", "language_server_macos_arm", "node", "", "OneDrive Sync Service"}
	kinds := []intel.StopKind{intel.StopAvoid, intel.StopTerm, intel.StopBrew, intel.StopAvoid}

	rows := make([]Row, n)
	for i := range rows {
		ports := []string{itoa(3000 + i)}
		if i%4 == 0 {
			ports = append(ports, itoa(4000+i))
		}
		cwd := "/"
		if i%3 == 0 {
			cwd = ""
		} else if i%5 == 0 {
			cwd = "/Users/me/code/some/deep/project"
		}
		sk := kinds[i%len(kinds)]
		label := ""
		if sk != intel.StopAvoid {
			label = "Send TERM to PID " + itoa(100+i)
		}
		rows[i] = Row{
			L: scan.Listener{PID: 100 + i, Ports: ports, Command: helpers[i%len(helpers)], CommandLine: "/Applications/X.app/Contents/MacOS/X", Cwd: cwd, CPU: float64(i%300) / 2},
			P: intel.Profile{Identity: names[i%len(names)], Source: owners[i%len(owners)], Risk: []intel.Risk{intel.Low, intel.Med, intel.High}[i%3], StopKind: sk, StopLabel: label},
		}
	}
	return rows
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestRandomKeySequencesHoldTheInvariants(t *testing.T) {
	// MANDATORY, not hygiene: the key list includes "f", which persists a
	// pinned port. Without this the test writes to the real
	// ~/Library/Application Support/le/favorites — which it did once, pinning
	// six synthetic ports into a live config and then breaking the sort tests,
	// because a pinned row floats to the top of every sort.
	stubFavoritesDir(t)

	keys := []string{
		"j", "k", "g", "G", "tab", "esc", "enter", "z", "Z", "f", "c", "x", "n",
		"1", "2", "3", "4", "5", "6", "7", "r", "?", "o", "F", "T", "left", "right",
	}
	sizes := []struct{ w, h int }{{80, 24}, {120, 40}, {200, 60}, {60, 12}, {130, 30}}
	counts := []int{0, 1, 2, 3, 17, 60}

	// Fixed seed so a failure is reproducible from the key trail the assertion
	// prints. Swept seeds 1-12 by hand while writing this; one is kept in CI
	// because the value is catching regressions, not burning minutes.
	// #nosec G404 -- a deterministic PRNG is the requirement here, not a
	// weakness; nothing about this test is security-sensitive.
	rng := rand.New(rand.NewSource(1))
	for _, size := range sizes {
		for _, n := range counts {
			var m tea.Model = New(Options{})
			m, _ = m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			m, _ = m.Update(scannedMsg{rows: varietyRows(n), at: time.Now()})

			var trail []string
			for step := 0; step < 120; step++ {
				k := keys[rng.Intn(len(keys))]
				trail = append(trail, k)
				m, _ = m.Update(key(k))
				checkInvariants(t, m.(model), trail)

				// A background scan can land at any moment and rebuild
				// everything under the cursor — the case that has broken
				// cursor state twice today.
				if rng.Intn(9) == 0 {
					m, _ = m.Update(scannedMsg{rows: varietyRows(rng.Intn(20)), at: time.Now().Add(time.Duration(step) * time.Second)})
					trail = append(trail, "<scan>")
					checkInvariants(t, m.(model), trail)
				}
				// So can a resize.
				if rng.Intn(15) == 0 {
					s2 := sizes[rng.Intn(len(sizes))]
					m, _ = m.Update(tea.WindowSizeMsg{Width: s2.w, Height: s2.h})
					trail = append(trail, "<resize>")
					checkInvariants(t, m.(model), trail)
				}
			}
		}
	}
}

// Filtering interacts with grouping (it force-expands) and with the cursor.
// Typed input is a separate code path from the key switch, so drive it too.
func TestFilterTypingHoldsTheInvariants(t *testing.T) {
	stubFavoritesDir(t)
	for _, grouped := range []bool{false, true} {
		var m tea.Model = New(Options{Group: grouped})
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		m, _ = m.Update(scannedMsg{rows: varietyRows(25), at: time.Now()})

		trail := []string{"/"}
		m, _ = m.Update(key("/"))
		for _, r := range "onedrive" {
			m, _ = m.Update(key(string(r)))
			trail = append(trail, string(r))
			checkInvariants(t, m.(model), trail)
		}
		// Backspace out again, one character at a time.
		for i := 0; i < 10; i++ {
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			trail = append(trail, "<bs>")
			checkInvariants(t, m.(model), trail)
		}
		m, _ = m.Update(key("esc"))
		checkInvariants(t, m.(model), append(trail, "esc"))
	}
}
