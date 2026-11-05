//go:build !windows

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the actual command tree and Execute's exit-code mapping in an
// isolated process, with lsof fixtures instead of the host's listener set.
func TestCollectorCommandProcess(t *testing.T) {
	if os.Getenv("LE_TEST_COLLECTOR_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"le"}, os.Args[i+1:]...)
			Execute("test")
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func runCollectorCommand(t *testing.T, script string, args ...string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	// #nosec G306 -- Owner-only executable fixture in private t.TempDir; execution is required to test command collection.
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	childArgs := append([]string{"-test.run=^TestCollectorCommandProcess$", "--"}, args...)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- Re-executes this test binary with a fixed helper test and discrete, controlled fixture command arguments.
	cmd := exec.CommandContext(ctx, binary, childArgs...)
	cmd.Env = append(os.Environ(), "LE_TEST_COLLECTOR_PROCESS=1", "PATH="+dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("le %s did not stop after its collector failed", strings.Join(args, " "))
	}
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("start command fixture: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func TestCommandsReportCollectorFailure(t *testing.T) {
	cases := [][]string{
		{"list"},
		{"list", "--json"},
		{"list", "node", "--json"},
		{"stop", "3000", "--dry-run", "--json"},
		{"stop", "--dir", "/fixture", "--dry-run", "--json"},
		{"restart", "3000"},
		{"watch", "3000", "--timeout", "10ms"},
		{"watch-all", "--interval", "10ms"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, code := runCollectorCommand(t, "printf 'private fixture diagnostic' >&2\nexit 2\n", args...)
			if code != exitFailure || !strings.Contains(stderr, "scan error: lsof:") {
				t.Errorf("le %s exit=%d stderr=%q, want exit 1 and collector error", strings.Join(args, " "), code, stderr)
			}
			if stdout != "" {
				t.Errorf("failed scan emitted a successful snapshot: %q", stdout)
			}
			if strings.Contains(stderr, "private fixture diagnostic") {
				t.Error("command exposed collector diagnostic data")
			}
		})
	}
}

func TestListJSONNoMatchesIsSuccess(t *testing.T) {
	stdout, stderr, code := runCollectorCommand(t, "exit 1\n", "list", "--json")
	if code != exitOK || strings.TrimSpace(stdout) != "[]" || stderr != "" {
		t.Errorf("empty list exit=%d stdout=%q stderr=%q, want successful []", code, stdout, stderr)
	}
}

func TestWatchAllPollingFailureDoesNotReportListenerDrop(t *testing.T) {
	script := `case " $* " in
	*" -iTCP "*)
		if [ -f "$0.state" ]; then
			printf 'fixture collector failed' >&2
			exit 2
		fi
		: > "$0.state"
		printf 'p2147483647\ncnode\nn127.0.0.1:3000\n'
		;;
esac
`
	stdout, stderr, code := runCollectorCommand(t, script, "watch-all", "--interval", "10ms")
	if code != exitFailure || !strings.Contains(stderr, "scan error: lsof:") {
		t.Errorf("watch-all exit=%d stderr=%q, want collector failure", code, stderr)
	}
	if !strings.Contains(stdout, "watching for listener changes") || strings.Contains(stdout, "\n- ") {
		t.Errorf("watch-all must start with a good snapshot and emit no false drop: %q", stdout)
	}
}
