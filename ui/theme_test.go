package ui

import "testing"

// Every palette must fill every role — a zero color would render invisible
// text in whatever theme shipped incomplete.
func TestThemesComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range themes {
		if p.name == "" {
			t.Fatal("theme with empty name")
		}
		if seen[p.name] {
			t.Fatalf("duplicate theme name %q", p.name)
		}
		seen[p.name] = true
		for role, c := range map[string]string{
			"brand": string(p.brand), "subtle": string(p.subtle), "green": string(p.green),
			"yellow": string(p.yellow), "red": string(p.red), "selBG": string(p.selBG), "fg": string(p.fg),
		} {
			if c == "" {
				t.Errorf("theme %q: role %s is empty", p.name, role)
			}
		}
	}
}

func TestApplyThemeByNameCaseInsensitive(t *testing.T) {
	defer applyThemeIdx(0)
	if !ApplyTheme("NORD") {
		t.Fatal("NORD should resolve to nord")
	}
	if currentTheme() != "nord" {
		t.Errorf("currentTheme = %q, want nord", currentTheme())
	}
	if ApplyTheme("no-such-theme") {
		t.Error("unknown theme must report false")
	}
	if currentTheme() != "nord" {
		t.Error("unknown theme must not change the active theme")
	}
}

// cycleTheme must visit every theme exactly once per lap and wrap around.
func TestCycleThemeVisitsAllAndWraps(t *testing.T) {
	defer applyThemeIdx(0)
	applyThemeIdx(0)
	seen := map[string]bool{"default": true}
	for i := 0; i < len(themes)-1; i++ {
		seen[cycleTheme()] = true
	}
	if len(seen) != len(themes) {
		t.Errorf("one lap visited %d themes, want %d", len(seen), len(themes))
	}
	if got := cycleTheme(); got != themes[0].name {
		t.Errorf("cycle after full lap = %q, want wrap to %q", got, themes[0].name)
	}
}

// The retro themes are ports of the mac app's NativeTheme set — app/CLI
// parity is a promise ("the same table, over SSH"), so a rename or removal
// on either side must fail loudly here.
func TestAppPortedThemesPresent(t *testing.T) {
	for _, name := range []string{
		"msdos", "system7", "gameboy", "phosphor",
		"amber", "paper", "blueprint", "vaporwave",
	} {
		if !ApplyTheme(name) {
			t.Errorf("app-ported theme %q missing", name)
		}
	}
	applyThemeIdx(0)
}
