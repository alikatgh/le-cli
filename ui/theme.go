// Themes for the TUI. A theme is a small palette of semantic roles — brand,
// dim text, the three risk colors, selection background, foreground — and
// every style the views use is rebuilt from the active palette. The style
// variables (titleSt, dimSt, …) stay package-level so the render code reads
// exactly as it did when there was one hardcoded palette.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type palette struct {
	name   string
	brand  lipgloss.Color // le, key hints, the stop command accent
	subtle lipgloss.Color // dim/secondary text
	green  lipgloss.Color // low risk / success
	yellow lipgloss.Color // medium risk / confirm prompt
	red    lipgloss.Color // high risk / errors
	selBG  lipgloss.Color // selected-row background
	fg     lipgloss.Color // primary text
}

// themes is ordered: `t` cycles through it top to bottom. "default" is the
// palette the TUI shipped with; "light" is for light-background terminals;
// "mono" drops the risk colors entirely (bold still marks high risk) for
// colorblind users and minimal setups.
var themes = []palette{
	{name: "default", brand: "#E0218A", subtle: "#8B949E", green: "#3FB950", yellow: "#D29922", red: "#F85149", selBG: "#23304A", fg: "#E6EDF3"},
	{name: "light", brand: "#BF3989", subtle: "#6E7781", green: "#1A7F37", yellow: "#9A6700", red: "#CF222E", selBG: "#B6E3FF", fg: "#24292F"},
	{name: "nord", brand: "#81A1C1", subtle: "#7B88A1", green: "#A3BE8C", yellow: "#EBCB8B", red: "#BF616A", selBG: "#3B4252", fg: "#ECEFF4"},
	{name: "dracula", brand: "#FF79C6", subtle: "#6272A4", green: "#50FA7B", yellow: "#F1FA8C", red: "#FF5555", selBG: "#44475A", fg: "#F8F8F2"},
	{name: "solarized", brand: "#D33682", subtle: "#586E75", green: "#859900", yellow: "#B58900", red: "#DC322F", selBG: "#073642", fg: "#93A1A1"},
	{name: "mono", brand: "#FFFFFF", subtle: "#8A8A8A", green: "#C0C0C0", yellow: "#C0C0C0", red: "#FFFFFF", selBG: "#3A3A3A", fg: "#C0C0C0"},
}

// The active palette's colors and the styles derived from them. Reassigned
// wholesale by applyTheme — always together, so a style can never mix roles
// from two palettes.
var (
	brand, subtle, green, yellow, red, selBG, fg      lipgloss.Color
	titleSt, dimSt, headSt, selSt, keySt, okSt, errSt lipgloss.Style
	boxSt                                             lipgloss.Style
	themeIdx                                          int
)

func init() { applyThemeIdx(0) }

func applyThemeIdx(i int) {
	p := themes[i]
	themeIdx = i
	brand, subtle, green, yellow, red, selBG, fg = p.brand, p.subtle, p.green, p.yellow, p.red, p.selBG, p.fg
	titleSt = lipgloss.NewStyle().Bold(true).Foreground(brand)
	dimSt = lipgloss.NewStyle().Foreground(subtle)
	headSt = lipgloss.NewStyle().Foreground(subtle).Bold(true)
	selSt = lipgloss.NewStyle().Background(selBG).Foreground(fg).Bold(true)
	keySt = lipgloss.NewStyle().Foreground(brand).Bold(true)
	boxSt = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle).Padding(0, 1)
	okSt = lipgloss.NewStyle().Foreground(green)
	errSt = lipgloss.NewStyle().Foreground(red)
}

// ApplyTheme activates the named theme (case-insensitive). Unknown names are
// left alone and reported false so the caller can warn BEFORE the TUI takes
// the alt-screen — the same lesson as config warnings.
func ApplyTheme(name string) bool {
	for i, p := range themes {
		if strings.EqualFold(p.name, name) {
			applyThemeIdx(i)
			return true
		}
	}
	return false
}

// cycleTheme advances to the next theme and returns its name (the `t` key).
func cycleTheme() string {
	applyThemeIdx((themeIdx + 1) % len(themes))
	return themes[themeIdx].name
}

// currentTheme returns the active theme's name (for the settings overlay).
func currentTheme() string { return themes[themeIdx].name }

// configPathHint is the path shown in the settings overlay and the theme
// flash. Display-only (XDG_CONFIG_HOME users know who they are) — resolving
// the real path would drag config into ui just for a hint string.
const configPathHint = "~/.config/le/config"

// ThemeNames lists every theme in cycle order, for help text and docs.
func ThemeNames() []string {
	names := make([]string, len(themes))
	for i, p := range themes {
		names[i] = p.name
	}
	return names
}
