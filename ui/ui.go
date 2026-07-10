// Package ui is the Bubble Tea TUI for le: a live, keyboard-driven view of
// localhost listeners with a detail pane and a guarded stop action.
package ui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/kill"
	"github.com/alikatgh/le-cli/scan"
)

// Sortable columns, in the same left-to-right order they appear in the
// table — number keys 1-6 select one directly, matching the table's own
// column order so the key-to-column mapping needs no legend to be obvious.
const (
	sortPort = iota
	sortPID
	sortWhat
	sortRisk
	sortOwner
	sortDir
	sortCPU // appended last so keys 1-6 keep their meaning; 7 sorts by CPU
)

// CPU% "hot" thresholds live in the scan package (single source, shared with
// le list). Hot rows render bold as well as red so the mono theme — which has
// no red hue — still flags them.
const (
	cpuWarmPct = scan.CPUWarmPct
	cpuHotPct  = scan.CPUHotPct
)

const defaultInterval = 3 * time.Second

// Options configures the TUI at launch.
type Options struct {
	Interval time.Duration // refresh cadence (0 = default)
	Filter   string        // initial filter text
	Theme    string        // theme name ("" = default; validated by the caller)
}

// Row pairs a listener with its computed profile.
type Row struct {
	L scan.Listener
	P intel.Profile
}

type scannedMsg struct {
	rows []Row
	at   time.Time
	err  error
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
	confirmed Row // the row pinned when the confirm dialog opened
	copyMenu  bool
	copyRow   Row // the row pinned when the copy picker opened
	flash     string
	flashErr  bool
	help      bool
	loading   bool
	lastScan  time.Time
	interval  time.Duration
	sortCol   int
	sortAsc   bool
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
	if opts.Theme != "" {
		ApplyTheme(opts.Theme) // unknown names warned by the caller pre-alt-screen
	}
	m := model{filter: ti, loading: true, interval: interval, sortCol: sortPort, sortAsc: true}
	m.applyFilter()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(scanCmd(), m.tickCmd())
}

func scanCmd() tea.Cmd {
	return func() tea.Msg {
		listeners, err := scan.Scan()
		env := intel.Detect()
		rows := make([]Row, len(listeners))
		for i, l := range listeners {
			rows[i] = Row{L: l, P: intel.Make(l, env)}
		}
		return scannedMsg{rows: rows, at: time.Now(), err: err}
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

// openURL and copyToClipboard are package vars so tests can observe the
// side effects without launching a browser or writing terminal escapes.
var (
	// openURL launches the default browser. `open` on macOS, `xdg-open`
	// on Linux — resolved at call time so tests can stub it.
	openURL = func(url string) error {
		cmd := "open"
		if runtime.GOOS != "darwin" {
			cmd = "xdg-open"
		}
		return exec.Command(cmd, url).Start()
	}
	// copyToClipboard emits an OSC 52 sequence, which the terminal — local
	// OR at the far end of an SSH session — translates into a clipboard
	// write. That's the point: pbcopy would only work on the machine le
	// runs on, and le explicitly supports being run on remote Linux boxes.
	// Written to stderr because Bubble Tea owns stdout; the terminal
	// consumes the escape either way and nothing is displayed.
	copyToClipboard = func(s string) error {
		_, err := osc52.New(s).WriteTo(os.Stderr)
		return err
	}
)

// stopCommand renders the selected row's stop action as a paste-able shell
// command. StopAvoid rows return false: suggesting a kill command for a
// process the app itself refuses to auto-stop would undercut the refusal.
func stopCommand(r Row) (string, bool) {
	switch r.P.StopKind {
	case intel.StopBrew:
		return "brew services stop " + r.P.StopArg, true
	case intel.StopDocker:
		// Copy the immutable container ID, not the reassignable name — a pasted
		// `docker stop <name>` can hit the wrong container after name reuse. (LE-420)
		id := r.P.StopArgID
		if id == "" {
			id = r.P.StopArg
		}
		return "docker stop " + id, true
	case intel.StopLaunchd:
		return "launchctl bootout " + intel.LaunchdDomainTarget(r.P.StopArg), true
	case intel.StopAvoid:
		return "", false
	default:
		return "kill -TERM " + strconv.Itoa(r.L.PID), true
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case scannedMsg:
		// Multiple scans can be in flight at once (tick, manual "r"
		// refresh, and the post-stop refresh in stopResultMsg below all
		// fire scanCmd() independently), and each stamps "at" only when
		// IT finishes — Bubble Tea delivers messages in whatever order
		// their goroutines happen to complete, which is not guaranteed
		// to match the order the scans started or the order their
		// timestamps would sort in. Applying an older result after a
		// newer one has already landed would roll the table back to
		// stale data, so drop anything not at least as recent as what's
		// currently displayed.
		if msg.at.Before(m.lastScan) {
			return m, nil
		}
		if msg.err != nil {
			// A genuine scan failure (scan.Scan already distinguishes lsof's
			// non-zero "empty match" exit from a real exec failure — LE-CLI-002),
			// so surface it instead of silently rendering an empty table as
			// "no listeners". Keep the last-good rows rather than blanking them,
			// and don't advance lastScan so a later success still applies. (R69)
			m.flash, m.flashErr = "scan failed: "+msg.err.Error(), true
			m.loading = false
			return m, nil
		}
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
			// Act on the row pinned when the dialog opened, NOT m.selected():
			// a background scan/tick can rebuild and re-sort the view while
			// the dialog is up, moving a different process under the cursor.
			// kill.Stop still re-verifies that pinned row's own PID.
			m.flash = ""
			return m, stopCmd(m.confirmed)
		case "n", "N", "esc", "q":
			m.confirm = false
		}
		return m, nil
	}

	// Copy picker captures keys next: pick which value of the pinned row to
	// yank. Mirrors the app's Copy URL / Copy curl / Copy lsof / Copy stop.
	if m.copyMenu {
		r := m.copyRow
		switch msg.String() {
		case "u":
			if url, ok := urlFor(r); ok {
				m.copyResult(url)
			} else {
				m.flash, m.flashErr = "no port to copy a URL for "+r.P.Identity, true
			}
			m.copyMenu = false
		case "r":
			if url, ok := urlFor(r); ok {
				m.copyResult("curl " + shellSingleQuote(url))
			} else {
				m.flash, m.flashErr = "no port to copy a curl for "+r.P.Identity, true
			}
			m.copyMenu = false
		case "l":
			if len(r.L.Ports) > 0 {
				m.copyResult("lsof -nP -iTCP:" + r.L.Ports[0] + " -sTCP:LISTEN")
			} else {
				m.flash, m.flashErr = "no port to copy an lsof for "+r.P.Identity, true
			}
			m.copyMenu = false
		case "s":
			if cmd, can := stopCommand(r); can {
				m.copyResult(cmd)
			} else {
				m.flash, m.flashErr = "no safe stop command for "+r.P.Identity+" — nothing copied", true
			}
			m.copyMenu = false
		case "p":
			// Parity with the app's "Copy ps": inspect the process behind the row.
			m.copyResult(fmt.Sprintf("ps -p %d -o pid,pcpu,pmem,lstart,command", r.L.PID))
			m.copyMenu = false
		case "esc", "q":
			m.copyMenu = false
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
	case "t":
		name := cycleTheme()
		m.flash, m.flashErr = "theme: "+name+"   (persist: theme = "+name+" in "+configPathHint+")", false
	case "1", "2", "3", "4", "5", "6", "7":
		col := int(msg.String()[0] - '1')
		if m.sortCol == col {
			m.sortAsc = !m.sortAsc
		} else {
			m.sortCol, m.sortAsc = col, true
		}
		m.sortView()
		m.clamp()
	case "x", "s":
		if r, ok := m.selected(); ok {
			if r.P.StopKind == intel.StopAvoid {
				m.flash, m.flashErr = "won't auto-stop "+r.P.Identity+" — inspect it first", true
			} else {
				m.confirm, m.confirmed = true, r // pin the row now; see the y/enter handler
			}
		}
	case "o":
		if r, ok := m.selected(); ok {
			if len(r.L.Ports) == 0 {
				m.flash, m.flashErr = "no port to open for "+r.P.Identity, true
			} else if err := openURL("http://localhost:" + r.L.Ports[0] + "/"); err != nil {
				m.flash, m.flashErr = "couldn't open browser: "+err.Error(), true
			} else {
				m.flash, m.flashErr = "opened http://localhost:"+r.L.Ports[0]+"/", false
			}
		}
	case "c":
		if r, ok := m.selected(); ok {
			m.copyMenu, m.copyRow, m.flash = true, r, ""
		}
	}
	return m, nil
}

// copyResult writes s to the clipboard (OSC 52) and reports the outcome in the
// flash line.
func (m *model) copyResult(s string) {
	if err := copyToClipboard(s); err != nil {
		m.flash, m.flashErr = "copy failed: "+err.Error(), true
	} else {
		m.flash, m.flashErr = "copied: "+s, false
	}
}

// urlFor is the browser URL for a row's first port, or false when it has none.
func urlFor(r Row) (string, bool) {
	if len(r.L.Ports) == 0 {
		return "", false
	}
	return "http://localhost:" + r.L.Ports[0] + "/", true
}

// shellSingleQuote wraps s so it survives as one argument in a pasted shell
// command (so `curl <url>` is safe even if a URL ever contains a shell char).
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
		// Only accept a click that lands on an actually-rendered row. Bounding
		// on len(m.view) instead of the visible window let a click in the empty
		// space below the last visible row select an off-screen row.
		end := m.offset + m.listHeight()
		if end > len(m.view) {
			end = len(m.view)
		}
		if idx := m.offset + (msg.Y - 2); msg.Y >= 2 && idx >= m.offset && idx < end {
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
	// Always build a fresh slice, even for the no-filter case: m.view is
	// sorted in place below, and aliasing it to m.all's backing array would
	// silently reorder m.all too (harmless today since a scan wholesale-
	// replaces m.all next cycle, but a landmine for any future code that
	// assumes m.all keeps its scan order).
	out := make([]Row, 0, len(m.all))
	if q == "" {
		out = append(out, m.all...)
	} else {
		for _, r := range m.all {
			hay := strings.ToLower(strings.Join([]string{
				strings.Join(r.L.Ports, " "), r.P.Identity, r.L.Command, r.L.CommandLine, r.L.Cwd, string(r.P.Source),
			}, " "))
			if strings.Contains(hay, q) {
				out = append(out, r)
			}
		}
	}
	m.view = out
	m.sortView()
}

// sortView sorts m.view by the active column/direction in place. Stable so
// ties (e.g. several rows at the same risk level) keep their prior relative
// order instead of jumping around on every resort.
func (m *model) sortView() {
	sort.SliceStable(m.view, func(i, j int) bool {
		if m.sortAsc {
			return m.less(m.view[i], m.view[j])
		}
		return m.less(m.view[j], m.view[i])
	})
}

func (m model) less(a, b Row) bool {
	switch m.sortCol {
	case sortPID:
		return a.L.PID < b.L.PID
	case sortWhat:
		return strings.ToLower(a.P.Identity) < strings.ToLower(b.P.Identity)
	case sortRisk:
		return riskRank(a.P.Risk) < riskRank(b.P.Risk)
	case sortOwner:
		return a.P.Source < b.P.Source
	case sortDir:
		return strings.ToLower(a.L.Cwd) < strings.ToLower(b.L.Cwd)
	case sortCPU:
		return a.L.CPU < b.L.CPU // 7 → ascending (idle first); press again for hottest-first
	default: // sortPort
		return firstPortNum(a.L.Ports) < firstPortNum(b.L.Ports)
	}
}

// cpuColor and cpuCell drive the TUI's CPU column. cpuCell renders the
// percent compactly ("818%"); cpuColor escalates dim → amber → red so a
// runaway is impossible to miss.
func cpuColor(pct float64) lipgloss.Color {
	switch {
	case pct >= cpuHotPct:
		return red
	case pct >= cpuWarmPct:
		return yellow
	default:
		return subtle
	}
}

func cpuCell(pct float64) string {
	if pct < 0.5 {
		return "·"
	}
	return fmt.Sprintf("%.0f%%", pct)
}

// fmtMem renders a resident-set size in KB as a human MB/GB string.
func fmtMem(rssKB int) string {
	if rssKB <= 0 {
		return "—"
	}
	mb := float64(rssKB) / 1024.0
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", mb/1024.0)
	}
	return fmt.Sprintf("%.0f MB", mb)
}

func riskRank(r intel.Risk) int {
	switch r {
	case intel.Low:
		return 0
	case intel.Med:
		return 1
	default: // intel.High
		return 2
	}
}

func firstPortNum(ports []string) int {
	if len(ports) == 0 {
		return 1 << 30 // sorts unlisted-port rows last
	}
	n, _ := strconv.Atoi(ports[0])
	return n
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

// ---- styles: see theme.go ----

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
		count = fmt.Sprintf("%d of %d · /%s", len(m.view), len(m.all), m.filter.Value())
	}
	// Risk pulse: elevated counts get their color in the header, so a
	// screenful of green never hides the two rows that matter.
	var hi, med int
	for _, r := range m.view {
		switch r.P.Risk {
		case intel.Low:
		case intel.Med:
			med++
		default:
			hi++
		}
	}
	pulse := ""
	if hi > 0 {
		pulse += errSt.Render(fmt.Sprintf("%d high", hi))
	}
	if med > 0 {
		if pulse != "" {
			pulse += dimSt.Render(" · ")
		}
		pulse += lipgloss.NewStyle().Foreground(yellow).Render(fmt.Sprintf("%d medium", med))
	}
	if pulse != "" {
		pulse += dimSt.Render("  ·  ")
	}
	when := "scanning…"
	if !m.lastScan.IsZero() {
		when = "updated " + m.lastScan.Format("15:04:05")
	}
	right := pulse + dimSt.Render(count+"  ·  "+when)
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

	// Every row leads with a 2-column risk gutter ("▎ ") — a color rail that
	// makes a screenful scannable without reading the RISK column.
	const gutterW = 2

	// The DIR column appears only when the terminal is wide enough to fit it
	// without starving WHAT/STOP — folders are the product's core "which
	// project is this?" signal, but never at the cost of an unreadable table.
	const dirW = 24
	const cpuW = 5        // fits "2400%" and the "CPU^" sort header
	showDir := m.w >= 124 // +6 vs the pre-CPU 118 to keep DIR from starving
	dirBudget := 0
	if showDir {
		dirBudget = dirW + 1
	}
	wWhat := clampInt(m.w-gutterW-8-1-7-1-cpuW-1-7-1-9-1-3-dirBudget, 14, 40)
	stopW := m.w - gutterW - wWhat - 36 - (cpuW + 1) - dirBudget
	var header string
	if showDir {
		header = fmt.Sprintf("%-8s %-7s %-*s %-*s %-*s %-7s %-9s %s",
			m.colLabel("PORT", sortPort), m.colLabel("PID", sortPID), cpuW, m.colLabel("CPU", sortCPU),
			wWhat, m.colLabel("WHAT", sortWhat),
			dirW, m.colLabel("DIR", sortDir), m.colLabel("RISK", sortRisk), m.colLabel("OWNER", sortOwner), "STOP")
	} else {
		header = fmt.Sprintf("%-8s %-7s %-*s %-*s %-7s %-9s %s",
			m.colLabel("PORT", sortPort), m.colLabel("PID", sortPID), cpuW, m.colLabel("CPU", sortCPU),
			wWhat, m.colLabel("WHAT", sortWhat),
			m.colLabel("RISK", sortRisk), m.colLabel("OWNER", sortOwner), "STOP")
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", gutterW) + headSt.Render(header) + "\n")

	rows := m.listHeight()
	end := m.offset + rows
	if end > len(m.view) {
		end = len(m.view)
	}
	for i := m.offset; i < end; i++ {
		r := m.view[i]
		// Cells are padded to DISPLAY width (padRight), not fmt's rune
		// count — %-*s under-pads CJK identities (8 cols in 4 runes) and
		// shifts every column after WHAT. Same trap truncate() was fixed for.
		// Make the port a ⌘-clickable link to its localhost URL. osc8 is
		// zero-width, so padRight still aligns the column to 8 display cells.
		portText := portCell(r.L.Ports)
		if url, ok := urlFor(r); ok {
			portText = osc8(portText, url)
		}
		port := padRight(portText, 8)
		pid := padRight(fmt.Sprintf("%d", r.L.PID), 7)
		cpu := padRight(cpuCell(r.L.CPU), cpuW)
		what := padRight(truncate(r.P.Identity, wWhat), wWhat)
		dir := ""
		if showDir {
			dir = padRight(dirCell(r.L.Cwd, homeDir, dirW), dirW) + " "
		}
		risk := padRight(string(r.P.Risk), 7)
		owner := padRight(truncate(string(r.P.Source), 9), 9)
		stop := truncate(stopShort(r.P), stopW)

		gutter := lipgloss.NewStyle().Foreground(riskColor(r.P.Risk)).Render("▎") + " "
		if i == m.cursor {
			line := port + " " + pid + " " + cpu + " " + what + " " + dir + risk + " " + owner + " " + stop
			b.WriteString(gutter + selSt.Width(m.w-gutterW).Render(line))
		} else {
			// Hierarchy: identity and the stop command read at full weight;
			// pid/dir/owner recede; risk and CPU carry their color (bold when
			// severe, so the mono theme — which has no red hue — still flags them).
			riskSt := lipgloss.NewStyle().Foreground(riskColor(r.P.Risk))
			if r.P.Risk != intel.Low && r.P.Risk != intel.Med {
				riskSt = riskSt.Bold(true)
			}
			cpuSt := lipgloss.NewStyle().Foreground(cpuColor(r.L.CPU))
			if r.L.CPU >= cpuHotPct {
				cpuSt = cpuSt.Bold(true)
			}
			b.WriteString(gutter +
				port + " " +
				dimSt.Render(pid) + " " +
				cpuSt.Render(cpu) + " " +
				what + " " +
				dimSt.Render(dir) +
				riskSt.Render(risk) + " " +
				dimSt.Render(owner) + " " +
				stop)
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// padRight pads s with spaces to display width n (no-op if already wider).
// Padding by lipgloss.Width, not rune count: a 4-rune CJK identity occupies
// 8 columns, and fmt's %-*s would under-pad it and shift every later column.
func padRight(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// colLabel appends a plain-ASCII direction marker to a column header when
// it's the active sort column, so the sort state is visible without opening
// help. ASCII only (never a wide/CJK glyph): this string feeds straight into
// a fmt "%-Ns" pad, which counts runes, not display width — a wide marker
// would throw off the same column alignment truncate() had to be fixed for.
func (m model) colLabel(name string, col int) string {
	if m.sortCol != col {
		return name
	}
	if m.sortAsc {
		return name + "^"
	}
	return name + "v"
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

// osc8 wraps text in an OSC 8 terminal hyperlink so terminals that support it
// (Ghostty, iTerm2, WezTerm, kitty…) let you ⌘-click the port to open url in a
// browser. Terminals that don't understand OSC 8 ignore the escape and show the
// text plainly. The escape carries ZERO display width, so column alignment is
// unaffected — TestOSC8LinkHasZeroDisplayWidth guards that invariant.
func osc8(text, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func (m model) detailView() string {
	r, ok := m.selected()
	if !ok {
		return boxSt.Width(m.w - 2).Height(detailHeight - 2).Render(dimSt.Render("select a listener"))
	}
	risk := lipgloss.NewStyle().Foreground(riskColor(r.P.Risk)).Bold(true).Render(strings.ToUpper(string(r.P.Risk)) + " risk")
	title := lipgloss.NewStyle().Bold(true).Foreground(fg).Render(r.P.Identity) +
		dimSt.Render(fmt.Sprintf("  (%d%% sure)  ", r.P.Confidence)) + risk
	cpuStr := lipgloss.NewStyle().Foreground(cpuColor(r.L.CPU)).Render(fmt.Sprintf("%.1f%%", r.L.CPU))
	if r.L.CPU >= cpuHotPct {
		cpuStr = lipgloss.NewStyle().Foreground(cpuColor(r.L.CPU)).Bold(true).Render(fmt.Sprintf("%.1f%%", r.L.CPU))
	}
	lines := []string{
		title,
		dimSt.Render("ports  ") + strings.Join(r.L.Ports, ", ") + dimSt.Render("   pid ") + fmt.Sprintf("%d", r.L.PID) + dimSt.Render("   owner ") + string(r.P.Source),
		dimSt.Render("usage  ") + cpuStr + dimSt.Render(" cpu   ") + fmtMem(r.L.RSS) + dimSt.Render(" mem"),
		dimSt.Render("cmd    ") + truncate(r.L.CommandLine, m.w-12),
		dimSt.Render("dir    ") + truncate(orDash(r.L.Cwd), m.w-12),
		dimSt.Render("stop   ") + lipgloss.NewStyle().Foreground(brand).Render(stopShort(r.P)) + dimSt.Render("   (c copies)"),
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
		// Describe the PINNED row, so the prompt and the action can't disagree
		// if the view shifted under the cursor while the dialog is open.
		r := m.confirmed
		q := lipgloss.NewStyle().Foreground(yellow).Render(
			fmt.Sprintf("⚠ Stop %s?  → %s   ", r.P.Identity, stopShort(r.P)))
		return q + keySt.Render("y") + dimSt.Render(" yes  ") + keySt.Render("n") + dimSt.Render(" no")
	}
	if m.copyMenu {
		label := lipgloss.NewStyle().Foreground(yellow).Render("copy " + m.copyRow.P.Identity + ":  ")
		opt := func(k, desc string) string { return keySt.Render(k) + dimSt.Render(" "+desc+"  ") }
		return label + opt("u", "url") + opt("r", "curl") + opt("l", "lsof") + opt("s", "stop") + opt("p", "ps") +
			keySt.Render("esc") + dimSt.Render(" cancel")
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
	keys := []string{"j/k move", "/ filter", "1-6 sort", "x stop", "o open", "c copy", "? help", "q quit"}
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
		{"1-7", "sort by port / pid / what / risk / owner / dir / cpu — press again to reverse"},
		{"x or s", "stop the selected listener (with confirm)"},
		{"o", "open http://localhost:<port>/ in the browser"},
		{"c", "copy… → u url · r curl · l lsof · s stop · p ps (OSC 52 — works over SSH)"},
		{"r", "refresh now"},
		{"t", "cycle theme (persist via config: theme = <name>)"},
		{"?", "toggle this help"},
		{"q", "quit"},
	}
	var b strings.Builder
	b.WriteString(titleSt.Render("le — keys") + "\n\n")
	for _, r := range rows {
		b.WriteString("  " + keySt.Render(fmt.Sprintf("%-8s", r[0])) + dimSt.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + dimSt.Render("  stop strategy is automatic: plain processes get TERM, Homebrew\n  services get `brew services stop`, containers get `docker stop`.\n  Every stop re-checks the PID first, so a recycled PID is never hit.\n"))
	b.WriteString("\n" + headSt.Render("  settings") + "\n")
	b.WriteString("  " + dimSt.Render(fmt.Sprintf("%-10s", "theme")) + currentTheme() + dimSt.Render("   (themes: "+strings.Join(ThemeNames(), " / ")+")") + "\n")
	b.WriteString("  " + dimSt.Render(fmt.Sprintf("%-10s", "refresh")) + m.interval.String() + "\n")
	b.WriteString("  " + dimSt.Render(fmt.Sprintf("%-10s", "config")) + configPathHint + dimSt.Render("   (interval / filter / theme)") + "\n")
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
	case intel.StopLaunchd:
		// Show the full domain target the stop actually uses, not the bare
		// label — the detail pane must match the copied command. (LE-419/427)
		return "launchctl bootout " + intel.LaunchdDomainTarget(p.StopArg)
	case intel.StopAvoid:
		return "avoid — inspect first"
	default:
		return "TERM"
	}
}

// homeDir is resolved once for the DIR column's ~-abbreviation.
var homeDir, _ = os.UserHomeDir()

// dirCell renders a working directory for the table: ~-abbreviated and
// clipped from the LEFT — a path's identity lives in its trailing
// components ("…/big-app/api" beats "/Users/me/co…"). Clips by DISPLAY
// width, not rune count: project paths can contain wide (CJK) runes, the
// same trap truncate() had to be fixed for.
func dirCell(cwd, home string, n int) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "-"
	}
	if home != "" {
		if cwd == home {
			cwd = "~"
		} else if strings.HasPrefix(cwd, home+"/") {
			cwd = "~" + cwd[len(home):]
		}
	}
	if lipgloss.Width(cwd) <= n {
		return cwd
	}
	r := []rune(cwd)
	width := 0
	budget := n - 1 // reserve 1 column for the leading ellipsis
	start := len(r)
	for i := len(r) - 1; i >= 0; i-- {
		rw := lipgloss.Width(string(r[i]))
		if width+rw > budget {
			break
		}
		width += rw
		start = i
	}
	return "…" + string(r[start:])
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
	// Accumulate by DISPLAY WIDTH, not rune count. A prior version of this
	// fix switched from byte-slicing to rune-slicing to stop cutting
	// multi-byte UTF-8 characters in half — that fixed the invalid-UTF-8
	// crash risk, but a wide rune (CJK, some emoji) still costs 2 terminal
	// columns while "costing" only 1 against a rune-count budget, so the
	// result could still overflow the caller's column budget by up to ~2x
	// (verified: a CJK container/directory name truncated to a rune count
	// of 8 rendered at 15 display columns). Walk runes one at a time,
	// tracking cumulative lipgloss.Width, so the output never exceeds n.
	var b strings.Builder
	width := 0
	budget := n - 1 // reserve 1 column for the ellipsis, itself 1 column wide
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if width+rw > budget {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + "…"
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
