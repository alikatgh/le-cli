package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// LE-411: `le stop --dry-run` must not promise to stop a StopAvoid row (a
// macOS/system helper) that a real stop refuses — it should mark it SKIP.
func TestPreviewMatchedSkipsStopAvoid(t *testing.T) {
	rows := []row{
		{Listener: scan.Listener{PID: 1}, Profile: intel.Profile{Identity: "node", StopKind: intel.StopTerm, StopLabel: "Send TERM to PID 1"}},
		{Listener: scan.Listener{PID: 2}, Profile: intel.Profile{Identity: "coreaudiod", StopKind: intel.StopAvoid}},
	}
	var buf bytes.Buffer
	previewMatched(&buf, rows)
	out := buf.String()

	if !strings.Contains(out, "would stop node") {
		t.Errorf("want 'would stop node', got: %q", out)
	}
	if strings.Contains(out, "would stop coreaudiod") {
		t.Errorf("must not offer to stop an avoid row: %q", out)
	}
	if !strings.Contains(out, "SKIP coreaudiod") {
		t.Errorf("want a SKIP line for the avoid row, got: %q", out)
	}
}
