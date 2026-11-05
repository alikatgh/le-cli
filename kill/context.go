package kill

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/internal/boundedexec"
	"github.com/alikatgh/le-cli/scan"
)

// ErrRefused distinguishes a failed identity/safety check from a command that
// was attempted and failed. It carries no subprocess output.
var ErrRefused = errors.New("action refused")

type contextOps struct {
	run     func(context.Context, string, ...string) (string, error)
	start   func(context.Context, int) (string, error)
	term    func(int) error
	brew    func(context.Context, string) (bool, error)
	docker  func(context.Context, string) (string, bool, error)
	launchd func(context.Context, string) (int, bool, error)
}

func defaultContextOps() contextOps {
	return contextOps{boundedexec.Output, startTimeContext, termProcess,
		intel.BrewServiceKnownContext, intel.DockerContainerIDContext, intel.LaunchdLabelPIDContext}
}

// StopContext is the stricter companion action seam. It requires a captured
// start identity, refuses unknown strategies, and never uses the legacy
// command-line fallback or permissive brew failure path.
func StopContext(ctx context.Context, l scan.Listener, p intel.Profile) (string, error) {
	return actionContext(ctx, l, p, false, defaultContextOps())
}

func RestartContext(ctx context.Context, l scan.Listener, p intel.Profile) (string, error) {
	return actionContext(ctx, l, p, true, defaultContextOps())
}

var managerArg = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_@./+:-]{0,255}$`)

func actionContext(ctx context.Context, l scan.Listener, p intel.Profile, restart bool, op contextOps) (string, error) {
	refuse := func(message string) (string, error) { return "", fmt.Errorf("%w: %s", ErrRefused, message) }
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p.StopKind == intel.StopDocker || intel.IsDockerHelper(l) {
		return refuse("Docker engine and local listener ownership are unverified; use Docker directly in the intended context")
	}
	if l.PID <= 1 || strings.TrimSpace(l.StartTime) == "" {
		return refuse("reliable process start identity unavailable; refresh")
	}
	if p.StopKind != intel.StopTerm && p.StopKind != intel.StopBrew && p.StopKind != intel.StopLaunchd {
		return refuse("no safe automatic action")
	}
	if restart && p.StopKind != intel.StopBrew {
		return refuse("restart requires a supported service manager")
	}
	start, err := op.start(ctx, l.PID)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return refuse("process identity could not be verified; refresh")
	}
	if normalizeWS(start) == "" || normalizeWS(start) != normalizeWS(l.StartTime) {
		return refuse("process exited or changed; refresh")
	}
	var command string
	var args []string
	verb := "stop"
	if restart {
		verb = "restart"
	}
	switch p.StopKind {
	case intel.StopTerm:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := op.term(l.PID); err != nil {
			return "", fmt.Errorf("signal failed: %w", err)
		}
		return termResult(l.PID), nil
	case intel.StopBrew:
		if !managerArg.MatchString(p.StopArg) {
			return refuse("service identifier unavailable")
		}
		known, err := op.brew(ctx, p.StopArg)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil || !known {
			return refuse("service manager could not verify the captured service; refresh")
		}
		// A manually launched Cellar binary can use the same formula as a
		// different registered service. Formula membership alone does not
		// establish that the selected process belongs to that service.
		pid, ok, err := op.launchd(ctx, "homebrew.mxcl."+p.StopArg)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil || !launchdGuardOK(l.PID, pid, ok) {
			return refuse("Homebrew user service no longer owns the selected process; refresh")
		}
		command, args = "brew", []string{"services", verb, p.StopArg}
	case intel.StopLaunchd:
		if !managerArg.MatchString(p.StopArg) {
			return refuse("service identifier unavailable")
		}
		pid, ok, err := op.launchd(ctx, p.StopArg)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil || !launchdGuardOK(l.PID, pid, ok) {
			return refuse("launchd identity changed or is unavailable; refresh")
		}
		command, args = "launchctl", []string{"bootout", intel.LaunchdDomainTarget(p.StopArg)}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := op.run(ctx, command, args...); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, boundedexec.ErrOutputLimit) {
			return "", err
		}
		return "", errors.New("service manager operation failed; refresh to inspect current state")
	}
	return "Service manager accepted the " + verb + " operation; refresh to inspect current state.", nil
}
