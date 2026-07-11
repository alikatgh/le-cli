package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Favorites (pinned ports): press `f` to pin the selected listener's port and
// it floats to the top of the table regardless of the active sort — "3000
// matters to me" survives restarts of whatever serves it. Persisted next to
// the config as ~/.config/le/favorites, one port per line, so they also
// survive le restarts. Mirrors the app's pinned ports.

// favoritesPath is a package var so tests can point it at a temp dir instead
// of the real user config.
var favoritesPath = func() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "le", "favorites"), nil
}

func loadFavorites() map[string]bool {
	favs := map[string]bool{}
	path, err := favoritesPath()
	if err != nil {
		return favs
	}
	data, err := os.ReadFile(path) // #nosec G304 -- fixed path under the user's own config dir
	if err != nil {
		return favs // missing file = no favorites; never an error
	}
	for _, line := range strings.Split(string(data), "\n") {
		if port := strings.TrimSpace(line); port != "" {
			favs[port] = true
		}
	}
	return favs
}

func saveFavorites(favs map[string]bool) error {
	path, err := favoritesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	ports := make([]string, 0, len(favs))
	for port, on := range favs {
		if on {
			ports = append(ports, port)
		}
	}
	sort.Strings(ports)
	return os.WriteFile(path, []byte(strings.Join(ports, "\n")+"\n"), 0o600)
}

// isFavorite reports whether any of the row's ports is pinned.
func (m model) isFavorite(r Row) bool {
	for _, port := range r.L.Ports {
		if m.favs[port] {
			return true
		}
	}
	return false
}
