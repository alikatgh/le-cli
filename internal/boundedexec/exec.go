// Package boundedexec executes fixed internal commands with cancellation and
// bounded output. It is deliberately not a remotely exposed command runner.
package boundedexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

const MaxStdout = 8 << 20
const MaxStderr = 16 << 10

var ErrOutputLimit = errors.New("subprocess output limit exceeded")

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > b.limit-b.buffer.Len() {
		p = p[:b.limit-b.buffer.Len()]
		b.exceeded = true
		b.cancel()
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

// Output never includes stderr in its returned text. ExitError.Stderr is
// retained for callers distinguishing a genuinely empty lsof exit from failure.
func Output(ctx context.Context, name string, args ...string) (string, error) {
	return run(ctx, name, args...)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = 300 * time.Millisecond
	stdout := &limitedBuffer{limit: MaxStdout, cancel: cancel}
	stderr := &limitedBuffer{limit: MaxStderr, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	configure(cmd)
	err := cmd.Run()
	// Also retire descendants when the direct process exited with inherited
	// pipes still open. WaitDelay alone closes pipes but does not kill children.
	retire(cmd)
	if stdout.exceeded || stderr.exceeded {
		return "", ErrOutputLimit
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitErr.Stderr = append([]byte(nil), stderr.buffer.Bytes()...)
	}
	return stdout.buffer.String(), err
}
