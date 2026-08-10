package ui

// This file exists to make it IMPOSSIBLE for a test to launch a real window.
//
// The three launcher hooks below shell out to the desktop: `open` a browser
// tab, `open -R` a Finder window, `open -a Terminal` a whole new login shell.
// Individual tests that exercise those actions stub them and assert on the
// captured argument. The randomised key-sequence tests do NOT stub anything —
// they press every key in the map, including "F" and "T" — and reach "o"'s
// action through tab+enter without pressing "o" at all — 120 times per
// case across 30 cases.
//
// That combination once fired several hundred real `open` calls: ~800 Terminal
// windows, hundreds of Finder windows at Macintosh HD, and dozens of Chrome
// tabs pointed at localhost ports, in a few minutes, on the developer's live
// desktop. See docs/BUG_JOURNAL.md.
//
// Stubbing at each call site is the fix that gets forgotten — it already was,
// twice (favorites, then this). So the guard lives here instead: this file is
// compiled ONLY into the test binary, its init runs before any test in the
// package, and it costs the shipped binary nothing. A new test cannot opt out
// of it by accident, which is the entire point.
//
// A test that genuinely wants to observe a launch still stubs the hook it
// cares about, exactly as before — it just starts from a no-op instead of from
// a live `exec.Command`.
func init() {
	openURL = func(string) error { return nil }
	revealPath = func(string) error { return nil }
	openTerminalAt = func(string) error { return nil }
}
