// Package config loads le's optional config file from
// $XDG_CONFIG_HOME/le/config (or ~/.config/le/config). The format is simple
// key = value lines; missing file or keys fall back to defaults.
//
//	interval = 2     # TUI refresh seconds (default 3)
//	filter   = node  # initial TUI filter (default none)
//	theme    = nord  # TUI theme (default / light / nord / dracula / solarized / mono)
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultIntervalSeconds = 3

type Config struct {
	IntervalSeconds int
	Filter          string
	Theme           string
}

// Interval is the TUI refresh cadence, clamped to a sane minimum.
func (c Config) Interval() time.Duration {
	if c.IntervalSeconds < 1 {
		return defaultIntervalSeconds * time.Second
	}
	return time.Duration(c.IntervalSeconds) * time.Second
}

// Path returns the resolved config file path (whether or not it exists).
func Path() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "le", "config")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "le", "config")
	}
	return filepath.Join(home, ".config", "le", "config")
}

// Load reads the config file, returning defaults if it's absent or malformed.
// A file that exists but can't be read for another reason (permissions, a
// directory sitting at that path) is NOT the same as "no config" — the
// second return value is a non-empty warning in that case. Load itself never
// prints: a bare stderr write here would be swallowed by the TUI's alt-screen
// switch before a user could ever read it (see docs/BUG_JOURNAL.md). The
// caller decides how/when to surface it.
func Load() (Config, string) {
	c := Config{IntervalSeconds: defaultIntervalSeconds}
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return c, ""
		}
		return c, fmt.Sprintf("le: could not read config at %s: %v", Path(), err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), unquote(strings.TrimSpace(val))
		switch key {
		case "interval":
			if n, err := strconv.Atoi(val); err == nil {
				c.IntervalSeconds = n
			}
		case "filter":
			c.Filter = val
		case "theme":
			c.Theme = val
		}
	}
	return c, ""
}

// unquote strips one matching pair of surrounding single or double quotes, so
// a naturally-written `filter = "node"` yields node, not the literal "node"
// (which — being a substring nothing contains — would silently match nothing).
func unquote(s string) string {
	if len(s) >= 2 {
		if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
			return s[1 : len(s)-1]
		}
	}
	return s
}
