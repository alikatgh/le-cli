package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Grouping the list by owner.
//
// A normal machine lists ~15 listeners of which ~12 are background helpers
// the user is explicitly told not to touch — eight app helpers, three editor
// language servers, a couple of macOS services. Flat, that reads as fifteen
// problems. Grouped, it reads as "three things I run, plus thirteen I don't",
// which is the actual shape of the situation.
//
// Off by default and toggled with `z` (or `group = true` in the config).
// Collapsing HIDES rows, and a tool whose job is "show me what's listening"
// must not decide on its own to stop showing you a port. The user opts in;
// once in, the groups that start collapsed are exactly the ones where every
// row is un-actionable, and an active filter expands everything so a search
// can never miss a match.

// listItem is one line of the list: either a group header or a listener row.
// The cursor indexes THESE, not m.view, because a header occupies a line and
// can be acted on (it toggles).
type listItem struct {
	header bool
	owner  string // group header: the owner name
	count  int    // group header: how many rows it holds
	hidden int    // group header: how many are hidden right now (0 when open)
	row    Row    // row item
}

// buildItems turns the sorted, filtered rows into the lines to render.
// Ungrouped, that is one item per row and the cursor behaves exactly as it
// did before grouping existed.
func (m *model) buildItems() {
	if !m.grouped {
		m.items = make([]listItem, 0, len(m.view))
		for _, r := range m.view {
			m.items = append(m.items, listItem{row: r})
		}
		return
	}

	// Group order follows the rows' existing sort order — the first time an
	// owner appears is where its group goes. That keeps whatever the active
	// sort means (lowest port first, hottest CPU first) true of the groups
	// too, instead of imposing a second, invisible ordering.
	var order []string
	byOwner := map[string][]Row{}
	for _, r := range m.view {
		owner := string(r.P.Source)
		if _, seen := byOwner[owner]; !seen {
			order = append(order, owner)
		}
		byOwner[owner] = append(byOwner[owner], r)
	}

	m.items = m.items[:0]
	for _, owner := range order {
		rows := byOwner[owner]
		collapsed := m.isCollapsed(owner, rows)
		hidden := 0
		if collapsed {
			hidden = len(rows)
		}
		m.items = append(m.items, listItem{header: true, owner: owner, count: len(rows), hidden: hidden})
		if collapsed {
			continue
		}
		for _, r := range rows {
			m.items = append(m.items, listItem{row: r})
		}
	}
}

// isCollapsed decides whether a group is currently folded.
func (m model) isCollapsed(owner string, rows []Row) bool {
	// A filter is a search: hiding a match would make the search lie.
	if strings.TrimSpace(m.filter.Value()) != "" {
		return false
	}
	// A pinned port is one the user explicitly asked to keep in sight.
	for _, r := range rows {
		if m.isFavorite(r) {
			return false
		}
	}
	if state, set := m.collapsed[owner]; set {
		return state
	}
	return defaultCollapsed(rows)
}

// defaultCollapsed folds a group only when there is nothing in it to act on
// and it is big enough to be in the way. A group holding even one stoppable
// listener stays open — that row is the reason you opened le.
func defaultCollapsed(rows []Row) bool {
	if len(rows) < 3 {
		return false
	}
	for _, r := range rows {
		if actionable(r) {
			return false
		}
	}
	return true
}

// toggleGroup folds or unfolds the group under the cursor.
func (m model) toggleGroup(owner string) model {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	var rows []Row
	for _, r := range m.view {
		if string(r.P.Source) == owner {
			rows = append(rows, r)
		}
	}
	m.collapsed[owner] = !m.isCollapsed(owner, rows)
	m.buildItems()
	m.clamp()
	return m
}

// setAllGroups folds or unfolds every group at once.
func (m model) setAllGroups(collapsed bool) model {
	m.collapsed = map[string]bool{}
	for _, r := range m.view {
		m.collapsed[string(r.P.Source)] = collapsed
	}
	m.buildItems()
	m.clamp()
	return m
}

// anyExpanded reports whether at least one group is currently open, so `Z`
// can be a single toggle rather than two keys the user has to choose between.
func (m model) anyExpanded() bool {
	for _, it := range m.items {
		if it.header && it.hidden == 0 {
			return true
		}
	}
	return false
}

// groupHeaderLine renders a group header: a disclosure marker, the owner, how
// many listeners it holds, and — when folded — the ports inside it. A fold
// that hides rows without summarising them just moves the problem, so the
// ports stay visible either way; "what is on 42050?" must never require
// unfolding first. The summary is part of the line in BOTH states, selected
// or not, because a highlight is not a reason to withhold information.
func (m model) groupHeaderLine(it listItem, selected bool, width int) string {
	marker := "▾"
	if it.hidden > 0 {
		marker = "▸"
	}
	head := fmt.Sprintf("%s %s", marker, it.owner)
	meta := fmt.Sprintf(" · %d", it.count)
	if it.hidden > 0 {
		meta += " · " + summarize(m.view, it.owner)
	}
	if selected {
		return selSt.Width(width).Render(head + meta)
	}
	return lipgloss.NewStyle().Bold(true).Render(head) + dimSt.Render(meta)
}

// summarize describes a folded group's contents in one line: the ports it is
// holding, so folding never costs you the answer to "what is on 42050?".
func summarize(view []Row, owner string) string {
	var ports []string
	for _, r := range view {
		if string(r.P.Source) != owner {
			continue
		}
		ports = append(ports, r.L.Ports...)
	}
	sort.Slice(ports, func(i, j int) bool { return firstPortNum(ports[i:i+1]) < firstPortNum(ports[j:j+1]) })
	const maxShown = 6
	more := 0
	if len(ports) > maxShown {
		more = len(ports) - maxShown
		ports = ports[:maxShown]
	}
	s := strings.Join(ports, ", ")
	if more > 0 {
		s += fmt.Sprintf(", +%d", more)
	}
	return s
}

// selectedGroup returns the owner under the cursor when the cursor is on a
// group header.
func (m model) selectedGroup() (string, bool) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return "", false
	}
	if it := m.items[m.cursor]; it.header {
		return it.owner, true
	}
	return "", false
}

// groupedHint is the footer's grouping affordance.
func (m model) groupedHint() string {
	if !m.grouped {
		return "z group"
	}
	return "z flat"
}

// focusRowByPID puts the cursor on the line holding pid, if it is visible.
// Row indices are not line indices once groups exist, so anything that moves
// a row around (pinning, re-sorting) has to re-find it here rather than reuse
// a position from m.view.
func (m *model) focusRowByPID(pid int) {
	for i, it := range m.items {
		if !it.header && it.row.L.PID == pid {
			m.cursor = i
			return
		}
	}
}
