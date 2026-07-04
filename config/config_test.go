package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`"node"`: "node",
		`'node'`: "node",
		`node`:   "node",
		`"node`:  `"node`, // unbalanced — left as-is
		`""`:     "",
		`"a'b"`:  "a'b", // inner mismatched quote preserved
		``:       "",
	}
	for in, want := range cases {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadStripsQuotesFromFilter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	leDir := filepath.Join(dir, "le")
	if err := os.MkdirAll(leDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leDir, "config"), []byte("filter = \"node\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, _ := Load()
	if c.Filter != "node" {
		t.Errorf("Filter = %q, want node (quotes stripped)", c.Filter)
	}
}

func TestLoadDefaultsWhenAbsent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, warning := Load()
	if c.IntervalSeconds != defaultIntervalSeconds || c.Filter != "" {
		t.Errorf("got %+v, want defaults", c)
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty for a merely-absent config", warning)
	}
}

func TestLoadParsesIntervalAndFilter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, "interval = 2\nfilter = node\n")
	c, warning := Load()
	if c.IntervalSeconds != 2 || c.Filter != "node" {
		t.Errorf("got %+v", c)
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty for a valid config", warning)
	}
}

func TestLoadIgnoresMalformedLinesFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeConfig(t, dir, "not a valid line\ninterval = notanumber\nfilter = node\n")
	c, _ := Load()
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
	if err := os.MkdirAll(filepath.Join(dir, "le", "config"), 0o750); err != nil {
		t.Fatal(err)
	}
	c, warning := Load()
	if c.IntervalSeconds != defaultIntervalSeconds {
		t.Errorf("got %+v, want defaults when config path is unreadable", c)
	}
	// Regression test for the sweep finding: this warning used to be printed
	// directly by Load(), invisibly, right before the TUI's alt-screen
	// switch hid it. Load must hand the warning BACK so the caller can
	// surface it before entering the TUI.
	if warning == "" {
		t.Error("warning should be non-empty when the config path exists but can't be read")
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
	if err := os.MkdirAll(leDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leDir, "config"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadTheme(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "le"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "le", "config"), []byte("theme = \"nord\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, warn := Load()
	if warn != "" {
		t.Fatalf("unexpected warning: %s", warn)
	}
	if c.Theme != "nord" {
		t.Errorf("Theme = %q, want nord (quotes stripped)", c.Theme)
	}
}
