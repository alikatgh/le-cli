package intel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

const launchdLookupTimeout = 3 * time.Second
const launchdLookupOutputLimit = 1 << 20

var errLaunchdOutputLimit = errors.New("launchd lookup output limit exceeded")

func launchdLabelPID(label string) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), launchdLookupTimeout)
	defer cancel()
	return lookupLaunchdLabelPID(label, func() (string, error) {
		return readLaunchdList(ctx, func(commandCtx context.Context) *exec.Cmd {
			// Fixed OS binary and arguments: the label is only compared with
			// parsed output, never used as a command or shell argument.
			return exec.CommandContext(commandCtx, "/bin/launchctl", "list")
		})
	})
}

func lookupLaunchdLabelPID(label string, read func() (string, error)) (int, bool) {
	out, err := read()
	if err != nil {
		return 0, false
	}
	found := 0
	for pid, currentLabel := range parseLaunchdList(out) {
		if currentLabel == label {
			if found != 0 {
				return 0, false
			}
			found = pid
		}
	}
	return found, found > 0
}

// The command factory is a private fixture seam; production always selects
// /bin/launchctl above. Failed, oversized or canceled reads publish no rows.
func readLaunchdList(ctx context.Context, command func(context.Context) *exec.Cmd) (string, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := command(childCtx)
	output := &launchdOutput{cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 300 * time.Millisecond
	err := cmd.Run()
	if output.exceeded {
		return "", errLaunchdOutputLimit
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", errors.New("launchd process lookup failed")
	}
	return output.buffer.String(), nil
}

type launchdOutput struct {
	buffer   bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (out *launchdOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > launchdLookupOutputLimit-out.buffer.Len() {
		p = p[:launchdLookupOutputLimit-out.buffer.Len()]
		out.exceeded = true
		out.cancel()
	}
	_, _ = out.buffer.Write(p)
	return n, nil
}
