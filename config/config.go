// Package config loads le's optional config file from
// $XDG_CONFIG_HOME/le/config (or ~/.config/le/config). The format is simple
// key = value lines; missing file or keys fall back to defaults.
//
//	interval = 2     # TUI refresh seconds (default 3)
//	filter   = node  # initial TUI filter (default none)
package config

import (
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
func Load() Config {
	c := Config{IntervalSeconds: defaultIntervalSeconds}
	data, err := os.ReadFile(Path())
	if err != nil {
		return c
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
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "interval":
			if n, err := strconv.Atoi(val); err == nil {
				c.IntervalSeconds = n
			}
		case "filter":
			c.Filter = val
		}
	}
	return c
}
