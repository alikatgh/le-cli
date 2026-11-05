//go:build !windows

package boundedexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCancellationRetiresOwnedDescendants(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child-pid")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Output(ctx, "/bin/sh", "-c", `sleep 30 & child=$!; echo "$child" > "$1"; wait`, "owned-fixture", pidPath)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatal("execution was not bounded", err, time.Since(start))
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
	defer checkCancel()
	state, _ := Output(checkCtx, "ps", "-p", strconv.Itoa(pid), "-o", "stat=")
	if syscall.Kill(pid, 0) == nil && !strings.HasPrefix(strings.TrimSpace(state), "Z") {
		t.Fatalf("owned child remains live: %d %q", pid, state)
	}
}

func TestOutputLimitsCancelWithoutReturningPartialData(t *testing.T) {
	for _, tc := range []struct{ name, script string }{{"stdout", `yes x`}, {"stderr", `yes x >&2`}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			out, err := Output(ctx, "/bin/sh", "-c", tc.script)
			if !errors.Is(err, ErrOutputLimit) || out != "" {
				t.Fatalf("out len=%d err=%v", len(out), err)
			}
		})
	}
}

func TestExitErrorRetainsBoundedStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Output(ctx, "/bin/sh", "-c", `printf diagnostic >&2; exit 1`)
	if err == nil {
		t.Fatal("missing exit error")
	}
}
