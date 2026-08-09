package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/alikatgh/le-cli/tools"
)

// le's exit codes are part of its contract with scripts (docs/COMPATIBILITY.md).
// Before these existed every failure exited 1, which made `le wait PORT -t 30s`
// unscriptable: a wait that timed out was indistinguishable from a typo'd flag
// or a scan that couldn't run, even though a CI script wants to retry the first
// and abort on the others.
const (
	exitOK      = 0   // success
	exitFailure = 1   // it ran, it failed (nothing listening, stop refused, …)
	exitUsage   = 2   // le was invoked wrong — bad flag, bad args, unknown command
	exitTimeout = 124 // a --timeout deadline elapsed; matches timeout(1)
)

// errUsage is the marker every usage error carries. It is never returned
// directly — usageError wraps the real (human-readable) error and answers
// errors.Is for this sentinel, so the message the user sees is unchanged.
var errUsage = errors.New("usage error")

// usageError tags an error as "you invoked le wrong" without altering its
// text. The embedded error supplies Error(); Is() makes errors.Is(err,
// errUsage) true; Unwrap keeps any wrapped sentinel underneath reachable.
type usageError struct{ error }

func (usageError) Is(target error) bool { return target == errUsage }

func (e usageError) Unwrap() error { return e.error }

// usage marks err as a usage error (nil stays nil, so it's safe to wrap a
// bare call site).
func usage(err error) error {
	if err == nil {
		return nil
	}
	return usageError{err}
}

// usageArgs wraps a Cobra positional-args validator so an arg-count or
// unknown-command failure exits 2 instead of 1. Root uses it too: with
// subcommands registered, cobra.NoArgs is what produces `unknown command "foo"
// for "le"`, so wrapping it covers both cases in one place.
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		return usage(v(c, args))
	}
}

// exitCodeFor maps a command error to le's exit code. Ordering matters:
// a usage error is checked before the generic fallback, and the timeout
// sentinel before both, since a timeout is the most specific thing a script
// wants to branch on.
func exitCodeFor(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, tools.ErrTimeout):
		return exitTimeout
	case errors.Is(err, errUsage), errors.Is(err, tools.ErrInvalidPort):
		return exitUsage
	default:
		return exitFailure
	}
}
