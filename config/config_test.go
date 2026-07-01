package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenAbsent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := Load()
	if c.IntervalSeconds != defaultIntervalSeconds || c.Filter != "" {
		t.Errorf("got %+v, want defaults", c)
	}
}

func TestLoadParsesIntervalAndFilter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, "interval = 2\nfilter = node\n")
	c := Load()
	if c.IntervalSeconds != 2 || c.Filter != "node" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadIgnoresMalformedLinesFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, "not a valid line\ninterval = notanumber\nfilter = node\n")
	c := Load()
	if c.IntervalSeconds != defaultIntervalSeconds {
		t.Errorf("interval should fall back to default on a bad value, got %d", c.IntervalSeconds)
	}
	if c.Filter != "node" {
		t.Errorf("filter = %q, want node", c.Filter)
	}
}

func TestLoadDoesNotPanicOnUnreadableExistingPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Make "config" a directory instead of a file, so ReadFile fails with
	// something other than "not exist" — Load must still return defaults,
	// not panic or crash the whole CLI over a malformed config path.
	if err := os.MkdirAll(filepath.Join(dir, "le", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := Load()
	if c.IntervalSeconds != defaultIntervalSeconds {
		t.Errorf("got %+v, want defaults when config path is unreadable", c)
	}
}

func TestIntervalClampsNonPositive(t *testing.T) {
	c := Config{IntervalSeconds: 0}
	if c.Interval().Seconds() != defaultIntervalSeconds {
		t.Errorf("Interval() = %v, want default", c.Interval())
	}
}

func writeConfig(t *testing.T, xdgDir, content string) {
	t.Helper()
	leDir := filepath.Join(xdgDir, "le")
	if err := os.MkdirAll(leDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leDir, "config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
