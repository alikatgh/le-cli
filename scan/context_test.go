//go:build !windows

package scan

import (
	"context"
	"errors"
	"testing"
)

func TestScanContextCancellationDoesNotUseLegacyHookOrPublishRows(t *testing.T) {
	original := runCmd
	t.Cleanup(func() { runCmd = original })
	runCmd = func(string, ...string) (string, error) {
		t.Fatal("cancellable scans must never mutate or use the legacy hook")
		return "", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := ScanContext(ctx)
	if !errors.Is(err, context.Canceled) || len(rows) != 0 {
		t.Fatalf("cancelled scan published rows: rows=%d err=%v", len(rows), err)
	}
}
