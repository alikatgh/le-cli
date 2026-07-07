package tools

import "testing"

// runSystem backs the flush-dns / restart-dock / restart-finder / sleep-display
// commands. We can't run the real ones in a test (they'd relaunch the Dock),
// but we can pin the helper's success/failure handling with harmless binaries.
func TestRunSystem(t *testing.T) {
	if err := runSystem("noop", "/usr/bin/true"); err != nil {
		t.Errorf("a zero-exit command should succeed, got %v", err)
	}
	if err := runSystem("noop", "/usr/bin/false"); err == nil {
		t.Error("a non-zero-exit command should return an error")
	}
	if err := runSystem("noop", "/nonexistent/xyzzy"); err == nil {
		t.Error("an unrunnable command should return an error")
	}
}
