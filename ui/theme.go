// Themes for the TUI. A theme is a small palette of semantic roles — brand,
// dim text, the three risk colors, selection background, foreground — and
// every style the views use is rebuilt from the active palette. The style
// variables (titleSt, dimSt, …) stay package-level so the render code reads
// exactly as it did when there was one hardcoded palette.
package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

type palette struct {
	name   string
	brand  color.Color // le, key hints, the stop command accent
	subtle color.Color // dim/secondary text
	green  color.Color // low risk / success
	yellow color.Color // medium risk / confirm prompt
	red    color.Color // high risk / errors
	selBG  color.Color // selected-row background
	fg     color.Color // primary text
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
	{name: "default", brand: lipgloss.Color("#E0218A"), subtle: lipgloss.Color("#8B949E"), green: lipgloss.Color("#3FB950"), yellow: lipgloss.Color("#D29922"), red: lipgloss.Color("#F85149"), selBG: lipgloss.Color("#23304A"), fg: lipgloss.Color("#E6EDF3")},
	{name: "light", brand: lipgloss.Color("#BF3989"), subtle: lipgloss.Color("#6E7781"), green: lipgloss.Color("#1A7F37"), yellow: lipgloss.Color("#9A6700"), red: lipgloss.Color("#CF222E"), selBG: lipgloss.Color("#B6E3FF"), fg: lipgloss.Color("#24292F")},
	{name: "nord", brand: lipgloss.Color("#81A1C1"), subtle: lipgloss.Color("#7B88A1"), green: lipgloss.Color("#A3BE8C"), yellow: lipgloss.Color("#EBCB8B"), red: lipgloss.Color("#BF616A"), selBG: lipgloss.Color("#3B4252"), fg: lipgloss.Color("#ECEFF4")},
	{name: "dracula", brand: lipgloss.Color("#FF79C6"), subtle: lipgloss.Color("#6272A4"), green: lipgloss.Color("#50FA7B"), yellow: lipgloss.Color("#F1FA8C"), red: lipgloss.Color("#FF5555"), selBG: lipgloss.Color("#44475A"), fg: lipgloss.Color("#F8F8F2")},
	{name: "solarized", brand: lipgloss.Color("#D33682"), subtle: lipgloss.Color("#586E75"), green: lipgloss.Color("#859900"), yellow: lipgloss.Color("#B58900"), red: lipgloss.Color("#DC322F"), selBG: lipgloss.Color("#073642"), fg: lipgloss.Color("#93A1A1")},
	{name: "mono", brand: lipgloss.Color("#FFFFFF"), subtle: lipgloss.Color("#8A8A8A"), green: lipgloss.Color("#C0C0C0"), yellow: lipgloss.Color("#C0C0C0"), red: lipgloss.Color("#FFFFFF"), selBG: lipgloss.Color("#3A3A3A"), fg: lipgloss.Color("#C0C0C0")},
	// Ports of the mac app's native themes — CGA hues for msdos (the Norton
	// Commander cursor bar is the cyan selBG), one-bit System 7, DMG-olive
	// gameboy (no yellow on a Game Boy — warm collapses to the mid green),
	// CRT phosphors, e-ink paper, cyanotype blueprint, and neon vaporwave.
	{name: "msdos", brand: lipgloss.Color("#00AAAA"), subtle: lipgloss.Color("#55FFFF"), green: lipgloss.Color("#55FF55"), yellow: lipgloss.Color("#FFFF55"), red: lipgloss.Color("#FF5555"), selBG: lipgloss.Color("#00AAAA"), fg: lipgloss.Color("#FFFFFF")},
	{name: "system7", brand: lipgloss.Color("#000000"), subtle: lipgloss.Color("#555555"), green: lipgloss.Color("#2E6B30"), yellow: lipgloss.Color("#7A5A00"), red: lipgloss.Color("#C81414"), selBG: lipgloss.Color("#C0C0C0"), fg: lipgloss.Color("#000000")},
	{name: "gameboy", brand: lipgloss.Color("#0F380F"), subtle: lipgloss.Color("#306230"), green: lipgloss.Color("#306230"), yellow: lipgloss.Color("#306230"), red: lipgloss.Color("#8B1E1E"), selBG: lipgloss.Color("#8BAC0F"), fg: lipgloss.Color("#0F380F")},
	{name: "phosphor", brand: lipgloss.Color("#33FF33"), subtle: lipgloss.Color("#1D9E1D"), green: lipgloss.Color("#33FF33"), yellow: lipgloss.Color("#B8E62E"), red: lipgloss.Color("#FF5252"), selBG: lipgloss.Color("#0F4A0F"), fg: lipgloss.Color("#33FF33")},
	{name: "amber", brand: lipgloss.Color("#FFB000"), subtle: lipgloss.Color("#A87400"), green: lipgloss.Color("#FFB000"), yellow: lipgloss.Color("#FFD25F"), red: lipgloss.Color("#FF5340"), selBG: lipgloss.Color("#4A3300"), fg: lipgloss.Color("#FFB000")},
	{name: "paper", brand: lipgloss.Color("#201B14"), subtle: lipgloss.Color("#6E675C"), green: lipgloss.Color("#3F6C42"), yellow: lipgloss.Color("#8A5A00"), red: lipgloss.Color("#B3261E"), selBG: lipgloss.Color("#E4DECF"), fg: lipgloss.Color("#201B14")},
	{name: "blueprint", brand: lipgloss.Color("#7FD4FF"), subtle: lipgloss.Color("#7FA3D1"), green: lipgloss.Color("#7FE3A8"), yellow: lipgloss.Color("#FFD166"), red: lipgloss.Color("#FF7A6E"), selBG: lipgloss.Color("#14406E"), fg: lipgloss.Color("#DCE9FB")},
	{name: "vaporwave", brand: lipgloss.Color("#00E5FF"), subtle: lipgloss.Color("#B48FD9"), green: lipgloss.Color("#3DFABC"), yellow: lipgloss.Color("#FF9E00"), red: lipgloss.Color("#FF2E63"), selBG: lipgloss.Color("#33196B"), fg: lipgloss.Color("#FFE9FB")},
}

// The active palette's colors and the styles derived from them. Reassigned
// wholesale by applyTheme — always together, so a style can never mix roles
// from two palettes.
var (
	brand, subtle, green, yellow, red, selBG, fg      color.Color
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
