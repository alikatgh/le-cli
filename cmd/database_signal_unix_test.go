//go:build !windows

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDatabaseSignalHelper(t *testing.T) {
	if os.Getenv("LE_DB_SIGNAL_HELPER") != "yes" {
		return
	}
	r := newRoot("fixture", false)
	r.SetArgs([]string{"db", "backup", "postgres", "--address", "127.0.0.1:1", "--database", "fixture", "--binary", os.Getenv("LE_DB_SIGNAL_BINARY"), "--output", os.Getenv("LE_DB_SIGNAL_OUTPUT"), "--timeout", "30s"})
	err := r.Execute()
	os.Exit(exitCodeFor(err))
}

func TestDatabaseInterruptRetiresBackupProcessGroup(t *testing.T) {
	root := t.TempDir()
	vendor := filepath.Join(root, "owned-vendor")
	output := filepath.Join(root, "partial.dump")
	script := "#!/bin/sh\nprintf '%s' \"$$\" > \"$0.pid\"\nsleep 30\n"
	// #nosec G306 -- Owner-only executable is required for this synthetic vendor fixture.
	if err := os.WriteFile(vendor, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// #nosec G204 -- Spawn only this test executable with a fixed fixture selector.
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDatabaseSignalHelper$")
	child.Env = []string{"PATH=/usr/bin:/bin", "GORACE=atexit_sleep_ms=0", "LE_DB_SIGNAL_HELPER=yes", "LE_DB_SIGNAL_BINARY=" + vendor, "LE_DB_SIGNAL_OUTPUT=" + output}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	var pid int
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		// #nosec G304 -- PID marker from this test's owned vendor fixture.
		b, err := os.ReadFile(vendor + ".pid")
		if err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("backup fixture did not start")
	}
	defer func() { _ = syscall.Kill(-pid, syscall.SIGKILL) }()
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("interrupted backup reported success")
	}
	if ctx.Err() != nil {
		t.Fatal("CLI did not handle interrupt promptly")
	}
	// The selected vendor is the process-group leader; cancellation must retire
	// it before the CLI returns, while leaving the incomplete output in place.
	if syscall.Kill(pid, 0) == nil {
		t.Fatal("owned backup process survived Ctrl-C")
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal("partial backup was not preserved")
	}
}
