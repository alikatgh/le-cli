//go:build !windows

package boundedexec

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStreamRetiresDescendantsHoldingOutput(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "child-pid")
	path := filepath.Join(root, "output.dump")
	// #nosec G304 -- Exclusive file in this test's owned temporary directory.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err = Stream(ctx, "/bin/sh", []string{"-c", `sleep 30 & echo "$!" > "$1"; printf fixture`, "owned-fixture", pidPath}, []string{"PATH=/usr/bin:/bin"}, f, 64)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("inherited pipe was not bounded: %v", err)
	}
	// #nosec G304 -- The PID file belongs to this test's child process.
	b, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
	defer checkCancel()
	state, _ := Output(checkCtx, "ps", "-p", strconv.Itoa(pid), "-o", "stat=")
	if syscall.Kill(pid, 0) == nil && !strings.HasPrefix(strings.TrimSpace(state), "Z") {
		t.Fatalf("owned descendant remains active: %d", pid)
	}
}
