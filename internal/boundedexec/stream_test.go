package boundedexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamOwnedHelper(t *testing.T) {
	if os.Getenv("LE_STREAM_FIXTURE") != "yes" {
		return
	}
	switch os.Getenv("LE_STREAM_MODE") {
	case "large":
		fmt.Print(strings.Repeat("x", 1024))
	case "wait":
		time.Sleep(10 * time.Second)
	case "fail":
		fmt.Fprint(os.Stderr, "fixture-password")
		os.Exit(3)
	default:
		fmt.Print("fixture-backup")
	}
	os.Exit(0)
}
func TestStreamLimitsAndCancellation(t *testing.T) {
	for _, mode := range []string{"ok", "large", "wait", "fail"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.dump")
			// #nosec G304 -- Exclusive output beneath this test's owned temporary directory.
			f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			timeout := 3 * time.Second
			if mode == "wait" {
				timeout = 150 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			start := time.Now()
			n, err := Stream(ctx, os.Args[0], []string{"-test.run=^TestStreamOwnedHelper$"}, append(os.Environ(), "LE_STREAM_FIXTURE=yes", "LE_STREAM_MODE="+mode, "GORACE=atexit_sleep_ms=0"), f, 32)
			if time.Since(start) > timeout+time.Second {
				t.Fatal("process did not stop promptly")
			}
			switch mode {
			case "ok":
				if err != nil || n != 14 {
					t.Fatalf("%d %v", n, err)
				}
			case "large":
				if !errors.Is(err, ErrOutputLimit) || n != 32 {
					t.Fatalf("%d %v", n, err)
				}
			case "wait":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			case "fail":
				if err == nil || strings.Contains(err.Error(), "fixture-password") {
					t.Fatal("missing/redaction error", err)
				}
			}
			info, err := os.Stat(path)
			if err != nil || info.Size() != n {
				t.Fatalf("partial file not preserved: %v", err)
			}
		})
	}
}
