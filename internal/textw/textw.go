// Package textw does terminal column arithmetic: padding and truncating by
// DISPLAY WIDTH rather than bytes or runes.
//
// It exists because the same bug was fixed twice in one package and then
// shipped a third time in another. A CJK rune costs 2 terminal columns while
// counting as 1 rune and 3 bytes, so byte- or rune-based padding silently
// misaligns every column to its right. The TUI learned this (LE-CLI: the
// truncate display-width fix); `le list` did not, and the moment intel began
// naming apps from their bundles — putting 企业微信 in the WHAT column — its
// table skewed by 4 columns on that row.
//
// One implementation, used by both renderers, so they cannot disagree again.
package textw

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Width returns the number of terminal columns s occupies.
func Width(s string) int { return lipgloss.Width(s) }

// Pad right-pads s to n display columns. A string already at or past n is
// returned unchanged — truncate first if the column must hold.
func Pad(s string, n int) string {
	w := Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// Truncate clips s to at most n display columns, appending "…" when it had to
// cut. Runes are accumulated one at a time against a width budget, so a wide
// rune can never push the result past n (a rune-count budget would allow
// roughly 2x overflow) and a multi-byte rune is never split.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n < 1 {
		return ""
	}
	if Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	var b strings.Builder
	width := 0
	budget := n - 1 // reserve one column for the ellipsis
	for _, r := range s {
		rw := Width(string(r))
		if width+rw > budget {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + "…"
}

// Cell truncates s to n columns and pads it back out to exactly n — the
// combination every table column actually wants.
func Cell(s string, n int) string { return Pad(Truncate(s, n), n) }
