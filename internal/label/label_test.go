package label

import (
	"strings"
	"testing"
)

// The real collision from a live machine: one editor owning three listeners,
// two of which run the same binary.
func TestDisambiguateRealCollision(t *testing.T) {
	got := Disambiguate([]Item{
		{Identity: "Antigravity IDE", Helper: "Electron"},
		{Identity: "Antigravity IDE", Helper: "language_server_macos_arm"},
		{Identity: "Antigravity IDE", Helper: "language_server_macos_arm"},
		{Identity: "OneDrive", Helper: "OneDrive Sync Service"},
		{Identity: "adb", Helper: "adb"},
	})
	want := []string{
		"Antigravity IDE · Electron",
		"Antigravity IDE · language_server",
		"Antigravity IDE · language_server",
		"OneDrive", // appears once: no suffix, however different the helper is
		"adb",      // helper == identity: nothing to add
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Rows that are the same kind of process keep the clean label — the port
// column already separates them, and repeating a suffix on every row costs
// width while adding nothing.
func TestIdenticalHelpersStayClean(t *testing.T) {
	got := Disambiguate([]Item{
		{Identity: "Antigravity IDE", Helper: "language_server"},
		{Identity: "Antigravity IDE", Helper: "language_server"},
	})
	for i, g := range got {
		if g != "Antigravity IDE" {
			t.Errorf("label %d = %q, want the plain identity", i, g)
		}
	}
}

func TestShortHelperTrimsPlatformNoise(t *testing.T) {
	cases := map[string]string{
		"language_server_macos_arm": "language_server",
		"agent-linux-x86_64":        "agent",
		"tool_darwin_arm64":         "tool",
		"plain":                     "plain",
		"":                          "",
	}
	for in, want := range cases {
		if got := shortHelper("SomeApp", in); got != want {
			t.Errorf("shortHelper(%q) = %q, want %q", in, got, want)
		}
	}
}

// A helper that IS the identity adds nothing; neither does one that's only
// the identity plus platform noise.
func TestShortHelperDropsEchoes(t *testing.T) {
	if got := shortHelper("Figma", "figma"); got != "" {
		t.Errorf("shortHelper = %q, want empty for a case-insensitive echo", got)
	}
	if got := shortHelper("agent", "agent_macos_arm"); got != "" {
		t.Errorf("shortHelper = %q, want empty once the noise is trimmed", got)
	}
}

// Order in, order out — the caller pairs labels with rows by index.
func TestDisambiguatePreservesOrder(t *testing.T) {
	items := []Item{
		{Identity: "B", Helper: "one"},
		{Identity: "A", Helper: "x"},
		{Identity: "B", Helper: "two"},
	}
	got := Disambiguate(items)
	if len(got) != 3 || got[1] != "A" {
		t.Fatalf("got %v", got)
	}
	if !strings.HasPrefix(got[0], "B · ") || !strings.HasPrefix(got[2], "B · ") {
		t.Errorf("colliding rows should both be suffixed: %v", got)
	}
}
