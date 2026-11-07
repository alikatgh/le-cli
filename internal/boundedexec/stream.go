package boundedexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

type streamWriter struct {
	file      *os.File
	remaining int64
	written   int64
	exceeded  bool
	cancel    context.CancelFunc
}

func (w *streamWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		data = data[:w.remaining]
		w.exceeded = true
		defer w.cancel()
	}
	n, err := w.file.Write(data)
	w.written += int64(n)
	w.remaining -= int64(n)
	if w.exceeded {
		return n, ErrOutputLimit
	}
	return n, err
}

// Stream runs one trusted vendor utility with explicit environment and bounded
// stdout to an already-created private regular file. Stderr is never retained.
// The deadline bounds the process, not OS-uninterruptible filesystem I/O.
func Stream(ctx context.Context, name string, args, env []string, file *os.File, max int64) (int64, error) {
	if file == nil || max <= 0 {
		return 0, errors.New("invalid stream destination or limit")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, errors.New("stream destination must be a regular file")
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// #nosec G204 G702 -- A local operator chooses the trusted vendor utility; all options are discrete argv, no shell.
	cmd := exec.CommandContext(childCtx, name, args...)
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 300 * time.Millisecond
	w := &streamWriter{file: file, remaining: max, cancel: cancel}
	cmd.Stdout = w
	configure(cmd)
	err = cmd.Run()
	retire(cmd)
	if w.exceeded {
		return w.written, ErrOutputLimit
	}
	if ctx.Err() != nil {
		return w.written, ctx.Err()
	}
	if err != nil {
		return w.written, errors.New("vendor backup process failed (diagnostics withheld)")
	}
	return w.written, nil
}
