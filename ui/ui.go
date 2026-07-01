// Package ui is the Bubble Tea TUI for le: a live, keyboard-driven view of
// localhost listeners with a detail pane and a guarded stop action.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/kill"
	"github.com/alikatgh/le-cli/scan"
)

const defaultInterval = 3 * time.Second

// Options configures the TUI at launch.
type Options struct {
	Interval time.Duration // refresh cadence (0 = default)
	Filter   string        // initial filter text
}

// Row pairs a listener with its computed profile.
type Row struct {
	L scan.Listener
	P intel.Profile
}

type scannedMsg struct {
	rows []Row
	at   time.Time
}
type tickMsg time.Time
type stopResultMsg struct {
	ok  string
	err error
}

type model struct {
	all       []Row
	view      []Row
	cursor    int
	offset    int
	w, h      int
	filtering bool
	filter    textinput.Model
	confirm   bool
	flash     string
	flashErr  bool
	help      bool
	loading   bool
	lastScan  time.Time
	interval  time.Duration
}

// New builds the initial model from launch options.
func New(opts Options) model {
	ti := textinput.New()
	ti.Placeholder = "filter by port, name, folder…"
	ti.Prompt = "/"
	ti.CharLimit = 64
	ti.SetValue(opts.Filter)
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	m := model{filter: ti, loading: true, interval: interval}
	m.applyFilter()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(scanCmd(), m.tickCmd())
}

func scanCmd() tea.Cmd {
	return func() tea.Msg {
		listeners, _ := scan.Scan()
		env := intel.Detect()
		rows := make([]Row, len(listeners))
		for i, l := range listeners {
			rows[i] = Row{L: l, P: intel.Make(l, env)}
		}
		return scannedMsg{rows: rows, at: time.Now()}
	}
}

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func stopCmd(r Row) tea.Cmd {
	return func() tea.Msg {
		ok, err := kill.Stop(r.L, r.P)
		return stopResultMsg{ok: ok, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case scannedMsg:
		m.all = msg.rows
		m.loading = false
		m.lastScan = msg.at
		m.applyFilter()
		m.clamp()
		return m, nil

	case tickMsg:
		return m, tea.Batch(scanCmd(), m.tickCmd())

	case stopResultMsg:
		if msg.err != nil {
			m.flash, m.flashErr = msg.err.Error(), true
		} else {
			m.flash, m.flashErr = "✓ "+msg.ok, false
		}
		return m, scanCmd()

	case tea.MouseMsg:
		return m.onMouse(msg)

	case tea.KeyMsg:
		return m.onKey(msg)
	}

	if m.filtering {
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirm dialog captures keys first.
	if m.confirm {
		switch msg.String() {
		case "y", "Y", "enter":
			m.confirm = false
			if r, ok := m.selected(); ok {
				m.flash = ""
				return m, stopCmd(r)
			}
		case "n", "N", "esc", "q":
			m.confirm = false
		}
		return m, nil
	}

	// Filter input captures keys while active.
	if m.filtering {
		switch msg.String() {
		case "enter", "esc":
			m.filtering = false
			m.filter.Blur()
			if msg.String() == "esc" {
				m.filter.SetValue("")
				m.applyFilter()
				m.clamp()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyFilter()
		m.clamp()
		return m, cmd
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.help = !m.help
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.cursor = 0
		m.clamp()
	case "G", "end":
		m.cursor = len(m.view) - 1
		m.clamp()
	case "/":
		m.filtering = true
		m.flash = ""
		return m, m.filter.Focus()
	case "r":
		m.flash, m.loading = "", true
		return m, scanCmd()
	case "x", "s":
		if r, ok := m.selected(); ok {
			if r.P.StopKind == intel.StopAvoid {
				m.flash, m.flashErr = "won't auto-stop "+r.P.Identity+" — inspect it first", true
			} else {
				m.confirm = true
			}
		}
	}
	return m, nil
}

// onMouse handles wheel scrolling and click-to-select. The table's first data
// row is at screen Y=2 (Y0 header, Y1 column header).
func (m model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.help {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.move(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.move(1)
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && !m.filtering && !m.confirm {
		if idx := m.offset + (msg.Y - 2); msg.Y >= 2 && idx >= 0 && idx < len(m.view) {
			m.cursor = idx
			m.clamp()
		}
	}
	return m, nil
}

func (m *model) move(d int) {
	if len(m.view) == 0 {
		return
	}
	m.cursor += d
	m.clamp()
}

func (m *model) clamp() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.view)-1 {
		m.cursor = len(m.view) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	rows := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		m.view = m.all
		return
	}
	var out []Row
	for _, r := range m.all {
		hay := strings.ToLower(strings.Join([]string{
			strings.Join(r.L.Ports, " "), r.P.Identity, r.L.Command, r.L.CommandLine, r.L.Cwd, string(r.P.Source),
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, r)
		}
	}
	m.view = out
}

func (m model) selected() (Row, bool) {
	if m.cursor >= 0 && m.cursor < len(m.view) {
		return m.view[m.cursor], true
	}
	return Row{}, false
}

// listHeight is how many table rows fit given the chrome around them.
func (m model) listHeight() int {
	h := m.h - 2 /*header*/ - 1 /*col head*/ - detailHeight - 1 /*footer*/
	if h < 3 {
		h = 3
	}
	return h
}

const detailHeight = 9

func (m model) View() string {
	if m.w == 0 {
		return "starting le…"
	}
	if m.help {
		return m.helpView()
	}

	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.tableView())
	b.WriteString("\n")
	b.WriteString(m.detailView())
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

// ---- styles ----

var (
	brand   = lipgloss.Color("#E0218A")
	subtle  = lipgloss.Color("#8B949E")
	green   = lipgloss.Color("#3FB950")
	yellow  = lipgloss.Color("#D29922")
	red     = lipgloss.Color("#F85149")
	selBG   = lipgloss.Color("#23304A")
	fg      = lipgloss.Color("#E6EDF3")
	titleSt = lipgloss.NewStyle().Bold(true).Foreground(brand)
	dimSt   = lipgloss.NewStyle().Foreground(subtle)
	headSt  = lipgloss.NewStyle().Foreground(subtle).Bold(true)
	selSt   = lipgloss.NewStyle().Background(selBG).Foreground(fg).Bold(true)
	keySt   = lipgloss.NewStyle().Foreground(brand).Bold(true)
	boxSt   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle).Padding(0, 1)
	okSt    = lipgloss.NewStyle().Foreground(green)
	errSt   = lipgloss.NewStyle().Foreground(red)
)

func riskColor(r intel.Risk) lipgloss.Color {
	switch r {
	case intel.Low:
		return green
	case intel.Med:
		return yellow
	default:
		return red
	}
}

func (m model) headerView() string {
	left := titleSt.Render("le") + dimSt.Render(" · localhost explorer")
	count := fmt.Sprintf("%d listening", len(m.view))
	if m.filter.Value() != "" {
		count = fmt.Sprintf("%d of %d", len(m.view), len(m.all))
	}
	when := "scanning…"
	if !m.lastScan.IsZero() {
		when = "updated " + m.lastScan.Format("15:04:05")
	}
	right := dimSt.Render(count + "  ·  " + when)
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m model) tableView() string {
	if m.loading && len(m.view) == 0 {
		return dimSt.Render("  scanning localhost…")
	}
	if len(m.view) == 0 {
		return dimSt.Render("  no listeners match.")
	}

	wWhat := clampInt(m.w-8-1-7-1-7-1-9-1-3, 14, 40)
	header := fmt.Sprintf("%-8s %-7s %-*s %-7s %-9s %s", "PORT", "PID", wWhat, "WHAT", "RISK", "OWNER", "STOP")
	var b strings.Builder
	b.WriteString(headSt.Render(header) + "\n")

	rows := m.listHeight()
	end := m.offset + rows
	if end > len(m.view) {
		end = len(m.view)
	}
	for i := m.offset; i < end; i++ {
		r := m.view[i]
		line := fmt.Sprintf("%-8s %-7d %-*s %-7s %-9s %s",
			portCell(r.L.Ports), r.L.PID, wWhat, truncate(r.P.Identity, wWhat),
			string(r.P.Risk), truncate(string(r.P.Source), 9), truncate(stopShort(r.P), m.w-wWhat-36))
		if i == m.cursor {
			b.WriteString(selSt.Width(m.w).Render(line))
		} else {
			// %-8s is a MINIMUM width, not a cap — a listener with many ports
			// (portCell -> "54321 +12") can render wider than 8, so the risk
			// color must split at the port cell's real width, not a fixed 8.
			portWidth := lipgloss.Width(portCell(r.L.Ports))
			if portWidth < 8 {
				portWidth = 8
			}
			b.WriteString(lipgloss.NewStyle().Foreground(riskColor(r.P.Risk)).Render(line[:portWidth]))
			b.WriteString(line[portWidth:])
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func portCell(ports []string) string {
	switch len(ports) {
	case 0:
		return "-"
	case 1:
		return ports[0]
	default:
		return fmt.Sprintf("%s +%d", ports[0], len(ports)-1)
	}
}

func (m model) detailView() string {
	r, ok := m.selected()
	if !ok {
		return boxSt.Width(m.w - 2).Height(detailHeight - 2).Render(dimSt.Render("select a listener"))
	}
	risk := lipgloss.NewStyle().Foreground(riskColor(r.P.Risk)).Bold(true).Render(strings.ToUpper(string(r.P.Risk)) + " risk")
	title := lipgloss.NewStyle().Bold(true).Foreground(fg).Render(r.P.Identity) +
		dimSt.Render(fmt.Sprintf("  (%d%% sure)  ", r.P.Confidence)) + risk
	lines := []string{
		title,
		dimSt.Render("ports  ") + strings.Join(r.L.Ports, ", ") + dimSt.Render("   pid ")+fmt.Sprintf("%d", r.L.PID)+dimSt.Render("   owner ")+string(r.P.Source),
		dimSt.Render("cmd    ") + truncate(r.L.CommandLine, m.w-12),
		dimSt.Render("dir    ") + truncate(orDash(r.L.Cwd), m.w-12),
		dimSt.Render("stop   ") + lipgloss.NewStyle().Foreground(brand).Render(stopShort(r.P)),
		dimSt.Render("back   ") + orDash(r.P.Restart),
	}
	if r.P.Warning != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(yellow).Render("⚠ "+r.P.Warning))
	} else if r.P.Note != "" {
		lines = append(lines, dimSt.Render(r.P.Note))
	}
	body := strings.Join(lines, "\n")
	return boxSt.Width(m.w - 2).Render(body)
}

func (m model) footerView() string {
	if m.confirm {
		r, _ := m.selected()
		q := lipgloss.NewStyle().Foreground(yellow).Render(
			fmt.Sprintf("Stop %s?  → %s   ", r.P.Identity, stopShort(r.P)))
		return q + keySt.Render("y") + dimSt.Render(" yes  ") + keySt.Render("n") + dimSt.Render(" no")
	}
	if m.filtering {
		return m.filter.View()
	}
	if m.flash != "" {
		if m.flashErr {
			return errSt.Render("✗ " + strings.TrimPrefix(m.flash, "✗ "))
		}
		return okSt.Render(m.flash)
	}
	keys := []string{"j/k move", "/ filter", "x stop", "r refresh", "? help", "q quit"}
	var parts []string
	for _, k := range keys {
		sp := strings.SplitN(k, " ", 2)
		parts = append(parts, keySt.Render(sp[0])+dimSt.Render(" "+sp[1]))
	}
	return strings.Join(parts, dimSt.Render("  ·  "))
}

func (m model) helpView() string {
	rows := [][2]string{
		{"j / ↓", "move down"},
		{"k / ↑", "move up"},
		{"g / G", "jump to top / bottom"},
		{"/", "filter (esc clears)"},
		{"x or s", "stop the selected listener (with confirm)"},
		{"r", "refresh now"},
		{"?", "toggle this help"},
		{"q", "quit"},
	}
	var b strings.Builder
	b.WriteString(titleSt.Render("le — keys") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + keySt.Render(fmt.Sprintf("%-8s", r[0])) + dimSt.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + dimSt.Render("  stop strategy is automatic: plain processes get TERM, Homebrew\n  services get `brew services stop`, containers get `docker stop`.\n  Every stop re-checks the PID first, so a recycled PID is never hit.\n"))
	b.WriteString("\n  " + keySt.Render("?") + dimSt.Render(" or ") + keySt.Render("q") + dimSt.Render(" to go back"))
	return boxSt.Render(b.String())
}

// ---- helpers ----

func stopShort(p intel.Profile) string {
	switch p.StopKind {
	case intel.StopBrew:
		return "brew services stop " + p.StopArg
	case intel.StopDocker:
		return "docker stop " + p.StopArg
	case intel.StopAvoid:
		return "avoid — inspect first"
	default:
		return "TERM"
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n < 1 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Run starts the TUI.
// Run launches the TUI with the given options.
func Run(opts Options) error {
	p := tea.NewProgram(New(opts), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
