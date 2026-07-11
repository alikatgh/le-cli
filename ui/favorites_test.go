package ui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// stubFavoritesDir points the favorites file at a temp dir for the test.
func stubFavoritesDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig := favoritesPath
	favoritesPath = func() (string, error) { return filepath.Join(dir, "favorites"), nil }
	t.Cleanup(func() { favoritesPath = orig })
}

func TestFavoritesRoundTrip(t *testing.T) {
	stubFavoritesDir(t)
	if got := loadFavorites(); len(got) != 0 {
		t.Fatalf("fresh dir should have no favorites, got %v", got)
	}
	if err := saveFavorites(map[string]bool{"3000": true, "8080": true}); err != nil {
		t.Fatal(err)
	}
	got := loadFavorites()
	if !got["3000"] || !got["8080"] || len(got) != 2 {
		t.Fatalf("round trip = %v, want 3000+8080", got)
	}
}

func TestPinKeyMovesRowToTopAndPersists(t *testing.T) {
	stubFavoritesDir(t)
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = m.Update(scannedMsg{rows: sampleRows(), at: time.Now()})

	// Port-ascending order is 3000, 5000, 27017 — move to the last row
	// (MongoDB :27017) and pin it.
	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("f"))

	mm := m.(model)
	if len(mm.view) == 0 || !mm.isFavorite(mm.view[0]) {
		t.Fatal("pinned row should be first in the view")
	}
	if mm.view[0].L.Ports[0] != "27017" {
		t.Fatalf("top row port = %v, want 27017", mm.view[0].L.Ports)
	}
	// Cursor follows the row it just pinned.
	if sel, ok := mm.selected(); !ok || sel.L.Ports[0] != "27017" {
		t.Fatal("cursor should stay on the toggled row")
	}
	// Persisted for the next session.
	if favs := loadFavorites(); !favs["27017"] {
		t.Fatalf("favorites file should contain 27017, got %v", favs)
	}

	// Unpin restores natural order (port-ascending → 3000 first).
	var mi tea.Model = mm
	mi, _ = mi.Update(key("f"))
	got := mi.(model)
	if got.view[0].L.Ports[0] != "3000" {
		t.Fatalf("after unpin, top row = %v, want 3000", got.view[0].L.Ports)
	}
	if favs := loadFavorites(); len(favs) != 0 {
		t.Fatalf("favorites file should be empty after unpin, got %v", favs)
	}
}

func TestFavoritesSurviveFilterAndSort(t *testing.T) {
	stubFavoritesDir(t)
	var m tea.Model = New(Options{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := m.(model)
	mm.favs = map[string]bool{"27017": true}
	var mi tea.Model = mm
	mi, _ = mi.Update(scannedMsg{rows: sampleRows(), at: time.Now()})

	// Pinned row leads even under a different sort column (2 = PID).
	mi, _ = mi.Update(key("2"))
	got := mi.(model)
	if got.view[0].L.Ports[0] != "27017" {
		t.Fatalf("pinned row should lead under PID sort, got %v", got.view[0].L.Ports)
	}
}
