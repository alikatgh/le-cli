package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

type deadlineBeforeSignal struct {
	context.Context
	deadline time.Time
}

func (c deadlineBeforeSignal) Deadline() (time.Time, bool) { return c.deadline, true }

func TestDatabaseDeadlineClassificationBeforeContextSignal(t *testing.T) {
	ctx := deadlineBeforeSignal{Context: context.Background(), deadline: time.Now().Add(-time.Millisecond)}
	if ctx.Err() != nil {
		t.Fatal("fixture must model an unpublished context timer")
	}
	if err := safeError(ctx, errors.New("sanitized driver failure")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("elapsed deadline lost: %v", err)
	}
	if err := safeError(context.Background(), fmt.Errorf("wrapped socket: %w", os.ErrDeadlineExceeded)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("socket deadline lost: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := safeError(cancelled, os.ErrDeadlineExceeded); !errors.Is(err, context.Canceled) {
		t.Fatalf("explicit cancellation changed: %v", err)
	}
	if !errors.Is(safeError(ctx, ErrLimit), ErrLimit) || !errors.Is(safeError(ctx, ErrConfig), ErrConfig) {
		t.Fatal("specific validation/limit failures were replaced")
	}
	if !errors.Is(safeError(context.Background(), errors.New("private diagnostic")), ErrQuery) {
		t.Fatal("ordinary failures must remain redacted")
	}
}
