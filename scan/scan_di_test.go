//go:build !windows

package scan

import (
	"strings"
	"testing"
)

// stubRunCmd swaps runCmd for a canned responder keyed on the command being
// run, letting Scan's full orchestration (lsof parse -> concurrent ps merge ->
// cwd lookup -> sort) run with no real subprocesses. Returns a restore func.
func stubRunCmd(t *testing.T, responder func(name string, args []string) string) func() {
	t.Helper()
	orig := runCmd
	runCmd = func(name string, args ...string) (string, error) {
		return responder(name, args), nil
	}
	return func() { runCmd = orig }
}

func TestScanEndToEnd(t *testing.T) {
	responder := func(name string, args []string) string {
		joined := strings.Join(args, " ")
		switch {
		case name == "lsof" && strings.Contains(joined, "-iTCP"):
			return "p1234\ncnode\nPTCP\nn127.0.0.1:3000\nTST=LISTEN\n" +
				"p5678\ncredis-server\nPTCP\nn*:6379\nTST=LISTEN\n"
		case name == "ps" && strings.Contains(joined, "lstart"):
			return "1234 Mon Jun 23 14:00:00 2026 alice\n" +
				"5678 Mon Jun 23 15:00:00 2026 root\n"
		case name == "ps" && strings.Contains(joined, "command"):
			return "1234 node /app/server.js\n" +
				"5678 /usr/local/bin/redis-server\n"
		case name == "lsof" && strings.Contains(joined, "cwd"):
			return "p1234\nn/Users/alice/app\np5678\nn/var/db/redis\n"
		}
		return ""
	}
	defer stubRunCmd(t, responder)()

	got, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Scan() returned %d listeners, want 2: %+v", len(got), got)
	}

	// Sorted by first port: 3000 (node) before 6379 (redis).
	node := got[0]
	if node.PID != 1234 || node.Command != "node" || node.CommandLine != "node /app/server.js" ||
		node.User != "alice" || node.StartTime != "Mon Jun 23 14:00:00 2026" ||
		node.Cwd != "/Users/alice/app" || len(node.Ports) != 1 || node.Ports[0] != "3000" {
		t.Errorf("node listener = %+v", node)
	}
	redis := got[1]
	if redis.PID != 5678 || redis.Command != "redis-server" ||
		redis.CommandLine != "/usr/local/bin/redis-server" || redis.User != "root" ||
		redis.StartTime != "Mon Jun 23 15:00:00 2026" || redis.Cwd != "/var/db/redis" ||
		len(redis.Ports) != 1 || redis.Ports[0] != "6379" {
		t.Errorf("redis listener = %+v", redis)
	}
}

func TestScanNoListeners(t *testing.T) {
	// lsof exits non-zero / empty when nothing is listening.
	defer stubRunCmd(t, func(name string, args []string) string { return "" })()
	got, err := Scan()
	if err != nil || got != nil {
		t.Errorf("Scan() with no listeners = (%v, %v), want (nil, nil)", got, err)
	}
}

func TestScanCommandLineFallsBackToLsofName(t *testing.T) {
	// When ps reports nothing for a PID (both calls silent), Scan falls back to
	// lsof's short command name for CommandLine rather than leaving it empty.
	responder := func(name string, args []string) string {
		joined := strings.Join(args, " ")
		switch {
		case name == "lsof" && strings.Contains(joined, "-iTCP"):
			return "p999\ncmyproc\nn127.0.0.1:4000\n"
		default:
			return "" // ps + cwd all silent for this pid
		}
	}
	defer stubRunCmd(t, responder)()

	got, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 listener, got %d", len(got))
	}
	if got[0].CommandLine != "myproc" {
		t.Errorf("CommandLine = %q, want fallback to lsof name 'myproc'", got[0].CommandLine)
	}
}
