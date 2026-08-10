package ui

import (
	"math/rand"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestRandomKeySequencesHoldTheInvariants sweeps realistic window sizes — the
// smallest it tries is 60x12. This file covers the other end: the sizes a
// terminal actually reports during a drag-resize, in a tmux pane being split,
// or in the frame or two before the first real WindowSizeMsg lands.
//
// Degenerate geometry is where a TUI renderer panics rather than merely
// looking wrong: a height that leaves no room for the list makes listHeight()
// go negative, a width narrower than a column's padding makes truncate/pad
// slice out of range. None of that is reachable from the sizes above.

// degenerateSizes are the ones with no room to spare. 0x0 is what bubbletea
// reports before the terminal answers, and it is a real frame the model may be
// asked to render.
var degenerateSizes = []struct{ w, h int }{
	{0, 0},
	{1, 1},
	{1, 60},
	{200, 1},
	{80, 0}, // positive width, zero height — a distinct path from 0x0
	{20, 3},
	{40, 5},
	{80, 9},  // exactly detailHeight — the list gets nothing
	{80, 10}, // one line more
}

func TestDegenerateTerminalSizesHoldTheInvariants(t *testing.T) {
	stubFavoritesDir(t)

	keys := []string{
		"j", "k", "g", "G", "tab", "esc", "enter", "z", "Z", "f", "c", "x", "n",
		"1", "2", "3", "r", "?", "o", "F", "T", "left", "right",
	}
	// #nosec G404 -- deterministic PRNG is the requirement, not a weakness.
	rng := rand.New(rand.NewSource(7))

	for _, size := range degenerateSizes {
		for _, n := range []int{0, 1, 17} {
			var m tea.Model = New(Options{})
			m, _ = m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			m, _ = m.Update(scannedMsg{rows: varietyRows(n), at: time.Now()})

			trail := []string{"<" + itoa(size.w) + "x" + itoa(size.h) + ">"}
			checkInvariantsAllowingEmptyView(t, m.(model), trail)

			for step := 0; step < 60; step++ {
				k := keys[rng.Intn(len(keys))]
				trail = append(trail, k)
				m, _ = m.Update(key(k))
				checkInvariantsAllowingEmptyView(t, m.(model), trail)

				// The resize that shrinks under a cursor already near the
				// bottom is the specific sequence that breaks offset math.
				if rng.Intn(6) == 0 {
					s2 := degenerateSizes[rng.Intn(len(degenerateSizes))]
					m, _ = m.Update(tea.WindowSizeMsg{Width: s2.w, Height: s2.h})
					trail = append(trail, "<resize>")
					checkInvariantsAllowingEmptyView(t, m.(model), trail)
				}
			}
		}
	}
}

// A window with no area legitimately has nothing to draw, so the non-empty
// view assertion is dropped for zero-area frames only. Every other invariant —
// cursor range, offset, selected() agreement, viewIdx bounds — still holds,
// and View() must still not panic.
func checkInvariantsAllowingEmptyView(t *testing.T, mm model, trail []string) {
	t.Helper()
	if mm.w <= 0 || mm.h <= 0 {
		_ = mm.View() // must not panic
		checkStateInvariants(t, mm, trail)
		return
	}
	checkInvariants(t, mm, trail)
}

// TestGrowingBackFromDegenerateRestoresAUsableView pins the recovery half: a
// pane squeezed to nothing and then restored has to render again. A model that
// clamped some size to zero permanently would pass every check above and still
// leave the user with a blank pane.
func TestGrowingBackFromDegenerateRestoresAUsableView(t *testing.T) {
	stubFavoritesDir(t)

	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(scannedMsg{rows: varietyRows(12), at: time.Now()})
	m, _ = m.Update(key("G")) // cursor to the bottom, where offset is non-zero

	for _, s := range degenerateSizes {
		m, _ = m.Update(tea.WindowSizeMsg{Width: s.w, Height: s.h})
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	if v := m.(model).View(); v == "" {
		t.Fatal("view stayed empty after growing back to 120x30")
	}
	checkInvariants(t, m.(model), []string{"<squeezed then restored>"})
}
