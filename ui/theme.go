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
//
// The retro block below ports the mac app's native themes (NativeTheme.swift)
// so the same identity travels between surfaces: a terminal owns its own
// canvas and typeface, so a TUI theme is only the seven semantic roles — the
// app's canvas colour and bundled raster fonts stay behind. selBG is the one
// role the app doesn't have; each is derived from that theme's canvas.
// system7, gameboy, and paper are dark-on-light like "light" — they want a
// light-background terminal.
var themes = []palette{
	{name: "default", brand: "#E0218A", subtle: "#8B949E", green: "#3FB950", yellow: "#D29922", red: "#F85149", selBG: "#23304A", fg: "#E6EDF3"},
	{name: "light", brand: "#BF3989", subtle: "#6E7781", green: "#1A7F37", yellow: "#9A6700", red: "#CF222E", selBG: "#B6E3FF", fg: "#24292F"},
	{name: "nord", brand: "#81A1C1", subtle: "#7B88A1", green: "#A3BE8C", yellow: "#EBCB8B", red: "#BF616A", selBG: "#3B4252", fg: "#ECEFF4"},
	{name: "dracula", brand: "#FF79C6", subtle: "#6272A4", green: "#50FA7B", yellow: "#F1FA8C", red: "#FF5555", selBG: "#44475A", fg: "#F8F8F2"},
	{name: "solarized", brand: "#D33682", subtle: "#586E75", green: "#859900", yellow: "#B58900", red: "#DC322F", selBG: "#073642", fg: "#93A1A1"},
	{name: "mono", brand: "#FFFFFF", subtle: "#8A8A8A", green: "#C0C0C0", yellow: "#C0C0C0", red: "#FFFFFF", selBG: "#3A3A3A", fg: "#C0C0C0"},
	// Ports of the mac app's native themes — CGA hues for msdos (the Norton
	// Commander cursor bar is the cyan selBG), one-bit System 7, DMG-olive
	// gameboy (no yellow on a Game Boy — warm collapses to the mid green),
	// CRT phosphors, e-ink paper, cyanotype blueprint, and neon vaporwave.
	{name: "msdos", brand: "#00AAAA", subtle: "#55FFFF", green: "#55FF55", yellow: "#FFFF55", red: "#FF5555", selBG: "#00AAAA", fg: "#FFFFFF"},
	{name: "system7", brand: "#000000", subtle: "#555555", green: "#2E6B30", yellow: "#7A5A00", red: "#C81414", selBG: "#C0C0C0", fg: "#000000"},
	{name: "gameboy", brand: "#0F380F", subtle: "#306230", green: "#306230", yellow: "#306230", red: "#8B1E1E", selBG: "#8BAC0F", fg: "#0F380F"},
	{name: "phosphor", brand: "#33FF33", subtle: "#1D9E1D", green: "#33FF33", yellow: "#B8E62E", red: "#FF5252", selBG: "#0F4A0F", fg: "#33FF33"},
	{name: "amber", brand: "#FFB000", subtle: "#A87400", green: "#FFB000", yellow: "#FFD25F", red: "#FF5340", selBG: "#4A3300", fg: "#FFB000"},
	{name: "paper", brand: "#201B14", subtle: "#6E675C", green: "#3F6C42", yellow: "#8A5A00", red: "#B3261E", selBG: "#E4DECF", fg: "#201B14"},
	{name: "blueprint", brand: "#7FD4FF", subtle: "#7FA3D1", green: "#7FE3A8", yellow: "#FFD166", red: "#FF7A6E", selBG: "#14406E", fg: "#DCE9FB"},
	{name: "vaporwave", brand: "#00E5FF", subtle: "#B48FD9", green: "#3DFABC", yellow: "#FF9E00", red: "#FF2E63", selBG: "#33196B", fg: "#FFE9FB"},
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
