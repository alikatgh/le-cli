package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alikatgh/le-cli/tools"
)

// The exit codes are a public contract (docs/COMPATIBILITY.md): a script that
// retries on 124 and aborts on 1 breaks silently if these drift.
func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"no error", nil, 0},
		{"generic failure", errors.New("nothing listening on 3000"), 1},
		{"wrapped generic failure", fmt.Errorf("stop: %w", errors.New("boom")), 1},
		{"wait timeout", fmt.Errorf("%w after 30s waiting for port 3000 to free", tools.ErrTimeout), 124},
		{"usage error", usage(errors.New("give a port, a pid, or --dir <path>")), 2},
		{"invalid port", fmt.Errorf("%w %q: expected a number 1-65535", tools.ErrInvalidPort, "99999"), 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCodeFor(c.err); got != c.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// usage must tag without rewriting: the user still reads the original text.
func TestUsagePreservesMessage(t *testing.T) {
	orig := errors.New("give a port, a pid, or --dir <path>")
	wrapped := usage(orig)
	if wrapped.Error() != orig.Error() {
		t.Errorf("usage() rewrote the message: %q, want %q", wrapped.Error(), orig.Error())
	}
	if !errors.Is(wrapped, orig) {
		t.Error("usage() must keep the wrapped error reachable via errors.Is")
	}
	if usage(nil) != nil {
		t.Error("usage(nil) must stay nil")
	}
}

// runRoot executes the command tree with args, discarding output, and returns
// the exit code le would have used.
func runRoot(t *testing.T, args ...string) int {
	t.Helper()
	root := newRoot("test")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	return exitCodeFor(root.Execute())
}

// The three ways a user can misinvoke le all have to reach exit 2 — that is
// the whole point of separating usage from failure.
func TestUsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"unknown command", []string{"nope"}},
		{"unknown flag", []string{"list", "--nope"}},
		{"too many args", []string{"list", "a", "b"}},
		{"missing required arg", []string{"wait"}},
		{"port/pid and --dir together", []string{"stop", "3000", "--dir", "/tmp"}},
		{"stop with neither", []string{"stop"}},
		{"bad keep-awake duration", []string{"keep-awake", "banana"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runRoot(t, c.args...); got != exitUsage {
				t.Errorf("le %s exited %d, want %d", strings.Join(c.args, " "), got, exitUsage)
			}
		})
	}
}

// An invalid port is caught before any waiting happens, so this is fast and
// safe to run in CI — and it proves the tools sentinel reaches the mapper.
func TestInvalidPortExitsTwo(t *testing.T) {
	for _, cmd := range []string{"wait", "ready"} {
		t.Run(cmd, func(t *testing.T) {
			if got := runRoot(t, cmd, "99999"); got != exitUsage {
				t.Errorf("le %s 99999 exited %d, want %d", cmd, got, exitUsage)
			}
		})
	}
}

// Grouping is user-visible structure, not decoration: an ungrouped command
// silently falls into "Additional Commands", which is exactly the flat list
// the groups were added to fix.
func TestEveryCommandIsGrouped(t *testing.T) {
	root := newRoot("test")
	// version is deliberately ungrouped; cobra adds help/completion itself.
	ungrouped := map[string]bool{"version": true, "help": true, "completion": true}
	groups := map[string]bool{}
	for _, g := range root.Groups() {
		groups[g.ID] = true
	}
	for _, c := range root.Commands() {
		if ungrouped[c.Name()] {
			continue
		}
		if c.GroupID == "" {
			t.Errorf("%q has no GroupID — it will render under 'Additional Commands'", c.Name())
			continue
		}
		if !groups[c.GroupID] {
			t.Errorf("%q is in group %q, which was never registered", c.Name(), c.GroupID)
		}
	}
}

// The exit-code contract has to be discoverable where script authors look:
// `le --help` and the man pages generated from these same Long strings.
func TestExitCodesDocumentedInHelp(t *testing.T) {
	root := newRoot("test")
	want := []string{"le", "wait", "ready", "watch", "open"}
	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			c := root
			if name != "le" {
				var found *cobra.Command
				for _, sub := range root.Commands() {
					if sub.Name() == name {
						found = sub
					}
				}
				if found == nil {
					t.Fatalf("no %q command", name)
				}
				c = found
			}
			if !strings.Contains(c.Long, "Exit codes:") {
				t.Errorf("%q's Long text doesn't document exit codes", name)
			}
		})
	}
}
