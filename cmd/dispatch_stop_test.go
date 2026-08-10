package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// dispatchStop is the shared tail of `le stop PORT` and `le stop --dir`, and it
// is where --dry-run either holds or fails catastrophically: the promise is
// "show me what you WOULD do", and breaking it kills the user's processes.
//
// The matrix is small and the consequence of one wrong branch is unbounded, so
// all four combinations are pinned, and the two dry-run rows assert the strong
// property — the stop function is never invoked — rather than merely checking
// what got printed.
func TestDispatchStopDryRunNeverStopsAnything(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		name := "text"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			matched := []row{sampleRow("3000", 100, "node"), sampleRow("5432", 101, "postgres")}
			called := 0
			stop := func(scan.Listener, intel.Profile) (string, error) {
				called++
				return "sent SIGTERM", nil
			}

			var out, errOut bytes.Buffer
			if err := dispatchStop(&out, &errOut, matched, true /* dryRun */, asJSON, stop); err != nil {
				t.Fatalf("dry-run returned %v, want nil", err)
			}

			if called != 0 {
				t.Fatalf("--dry-run invoked the stop function %d time(s) — it must never touch anything", called)
			}
			// A preview that prints nothing is also a broken promise: the user
			// asked what would happen and got silence.
			if out.Len() == 0 {
				t.Error("--dry-run printed nothing; the preview is the entire point")
			}
			if !strings.Contains(out.String(), "3000") || !strings.Contains(out.String(), "5432") {
				t.Errorf("preview does not name both matched ports:\n%s", out.String())
			}
		})
	}
}

// The other half of the matrix: without --dry-run, every matched listener must
// actually be handed to stop. A dispatch that quietly skipped rows would look
// identical to success from the outside.
func TestDispatchStopWithoutDryRunStopsEveryMatch(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		name := "text"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			matched := []row{sampleRow("3000", 100, "node"), sampleRow("5432", 101, "postgres")}
			var gotPIDs []int
			stop := func(l scan.Listener, _ intel.Profile) (string, error) {
				gotPIDs = append(gotPIDs, l.PID)
				return "sent SIGTERM", nil
			}

			var out, errOut bytes.Buffer
			if err := dispatchStop(&out, &errOut, matched, false /* dryRun */, asJSON, stop); err != nil {
				t.Fatalf("dispatchStop returned %v, want nil", err)
			}

			if len(gotPIDs) != 2 {
				t.Fatalf("stop called for %d listeners (%v), want 2", len(gotPIDs), gotPIDs)
			}
			if gotPIDs[0] != 100 || gotPIDs[1] != 101 {
				t.Errorf("stop called with pids %v, want [100 101]", gotPIDs)
			}
			// The port belongs in the result line for the same reason it
			// belongs in the preview: two same-named processes are otherwise
			// distinguishable only by PID.
			if !strings.Contains(out.String(), "3000") || !strings.Contains(out.String(), "5432") {
				t.Errorf("stop output does not name both ports:\n%s", out.String())
			}
		})
	}
}

// A listener with no ports must not render a dangling " on ". The case is real:
// scan keeps Ports non-nil but it can be empty when a listener's address does
// not parse.
func TestPortSuffixOmitsTheClauseWhenThereAreNoPorts(t *testing.T) {
	r := sampleRow("3000", 100, "node")
	r.Ports = nil
	if got := portSuffix(r); got != "" {
		t.Errorf("portSuffix with no ports = %q, want empty", got)
	}

	r.Ports = []string{"3000", "5432"}
	if got, want := portSuffix(r), " on 3000, 5432"; got != want {
		t.Errorf("portSuffix = %q, want %q", got, want)
	}
}

// A failing stop must surface as an error from dispatchStop, not be swallowed
// into a zero exit code — scripts branch on that.
func TestDispatchStopReportsFailures(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		name := "text"
		if asJSON {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			matched := []row{sampleRow("3000", 100, "node")}
			stop := func(scan.Listener, intel.Profile) (string, error) {
				return "", errors.New("permission denied")
			}

			var out, errOut bytes.Buffer
			if err := dispatchStop(&out, &errOut, matched, false, asJSON, stop); err == nil {
				t.Fatal("dispatchStop returned nil after every stop failed, want an error")
			}
		})
	}
}
