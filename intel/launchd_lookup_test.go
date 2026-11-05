package intel

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLaunchdOwnershipLookupRequiresOneSuccessfulExactJob(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		err          error
		pid          int
		ok           bool
	}{
		{"exact", "PID\tStatus\tLabel\n43\t0\thomebrew.mxcl.redis\n", nil, 43, true},
		{"different formula", "PID\tStatus\tLabel\n43\t0\thomebrew.mxcl.redis-other\n", nil, 0, false},
		{"not running", "PID\tStatus\tLabel\n-\t0\thomebrew.mxcl.redis\n", nil, 0, false},
		{"ambiguous", "PID\tStatus\tLabel\n43\t0\thomebrew.mxcl.redis\n44\t0\thomebrew.mxcl.redis\n", nil, 0, false},
		{"failed partial output", "PID\tStatus\tLabel\n43\t0\thomebrew.mxcl.redis\n", errors.New("private subprocess diagnostics"), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid, ok := lookupLaunchdLabelPID("homebrew.mxcl.redis", func() (string, error) { return tc.output, tc.err })
			if pid != tc.pid || ok != tc.ok {
				t.Fatalf("pid=%d ok=%v, want %d/%v", pid, ok, tc.pid, tc.ok)
			}
		})
	}
}

func TestLaunchdLookupDeadlineAndOutputCaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owned Unix subprocess fixtures")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	start := time.Now()
	output, err := readLaunchdList(ctx, func(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, "/bin/sleep", "30") })
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || output != "" || time.Since(start) > time.Second {
		t.Fatalf("deadline output bytes=%d err=%v elapsed=%v", len(output), err, time.Since(start))
	}
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err = readLaunchdList(ctx, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "/usr/bin/yes", "owned-output-fixture")
	})
	if !errors.Is(err, errLaunchdOutputLimit) || output != "" {
		t.Fatalf("limit output bytes=%d err=%v", len(output), err)
	}
}

func TestLaunchdLookupFailureReturnsGenericDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owned Unix subprocess fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := readLaunchdList(ctx, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "/bin/sh", "-c", "printf private-diagnostic >&2; exit 1")
	})
	if output != "" || err == nil || strings.Contains(err.Error(), "private-diagnostic") {
		t.Fatal("lookup failure disclosed diagnostics")
	}
}
