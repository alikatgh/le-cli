package intel

import (
	"context"
	"errors"
	"strings"

	"github.com/alikatgh/le-cli/internal/boundedexec"
)

// DetectContext is Detect's bounded, cancellable counterpart. Missing optional
// managers remain unavailable, but cancellation/output exhaustion never publishes
// a successful partial result.
func DetectContext(ctx context.Context) (Env, error) {
	e := Env{}
	brew, err := boundedexec.Output(ctx, "brew", "services", "list")
	if fatalContextError(ctx, err) != nil {
		return e, fatalContextError(ctx, err)
	}
	if err != nil {
		brew = ""
	}
	e.BrewStarted = parseBrewStarted(brew)
	// Numeric port matches cannot establish local Docker ownership.
	// Do not query the ambient (possibly remote) engine.
	e.DockerByPort = map[string]dockerContainer{}
	launchd, err := boundedexec.Output(ctx, "launchctl", "list")
	if fatalContextError(ctx, err) != nil {
		return e, fatalContextError(ctx, err)
	}
	if err != nil {
		launchd = ""
	}
	e.LaunchdByPID = parseLaunchdList(launchd)
	return e, nil
}

func fatalContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, boundedexec.ErrOutputLimit) {
		return err
	}
	return nil
}

func DockerContainerIDContext(ctx context.Context, name string) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return "", false, errors.New("docker engine and local listener ownership are unverified")
}

func LaunchdLabelPIDContext(ctx context.Context, label string) (int, bool, error) {
	out, err := boundedexec.Output(ctx, "launchctl", "list")
	if err != nil {
		return 0, false, err
	}
	for pid, candidate := range parseLaunchdList(out) {
		if candidate == label {
			return pid, true, nil
		}
	}
	return 0, false, nil
}

// Unlike the legacy best-effort helper, an unusable brew command fails closed.
func BrewServiceKnownContext(ctx context.Context, formula string) (bool, error) {
	out, err := boundedexec.Output(ctx, "brew", "services", "list")
	if err != nil {
		return false, err
	}
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if i > 0 && len(fields) > 0 && fields[0] == formula {
			return true, nil
		}
	}
	return false, nil
}
