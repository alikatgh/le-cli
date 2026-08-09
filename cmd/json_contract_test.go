package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

var errStopFailed = errors.New("permission denied")

// `le list --json` and `le stop --json` are the machine-readable surface other
// people build on (docs/COMPATIBILITY.md). A renamed or dropped key is a
// breaking change for every jq pipeline in the wild, and it is the kind of
// change that looks harmless in review — a struct-tag tweak three packages
// away. These goldens make it fail here instead of in someone's CI.
//
// Adding a key is fine: append it to the list, in the same commit.
// Removing or renaming one requires a major-version note in COMPATIBILITY.md.

var listRowKeys = []string{
	"addrs", "command", "commandLine", "cpu", "cwd", "pid", "ports",
	"profile", "rss", "startTime", "user",
}

var profileKeys = []string{
	"confidence", "explain", "identity", "kind", "note", "restart", "risk",
	"source", "stopArg", "stopArgID", "stopKind", "stopLabel", "warning",
}

var stopResultKeys = []string{
	"action", "dryRun", "identity", "ok", "pid", "ports",
}

func keysOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not a JSON object: %v", err)
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertKeys(t *testing.T, what string, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s JSON keys changed — this breaks scripts.\n got: %v\nwant: %v\n"+
			"If the change is intentional, update docs/COMPATIBILITY.md in the same commit.",
			what, got, want)
	}
}

func TestListJSONKeysAreStable(t *testing.T) {
	var buf bytes.Buffer
	if err := printJSON(&buf, []row{sampleRow("3000", 42, "node")}); err != nil {
		t.Fatal(err)
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("le list --json must emit a JSON array: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	assertKeys(t, "le list --json row", keysOf(t, rows[0]), listRowKeys)

	var row map[string]json.RawMessage
	if err := json.Unmarshal(rows[0], &row); err != nil {
		t.Fatal(err)
	}
	assertKeys(t, "le list --json row.profile", keysOf(t, row["profile"]), profileKeys)
}

func TestStopJSONKeysAreStable(t *testing.T) {
	var buf bytes.Buffer
	if err := previewMatchedJSON(&buf, []row{sampleRow("3000", 42, "node")}); err != nil {
		t.Fatal(err)
	}
	var results []json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &results); err != nil {
		t.Fatalf("le stop --json must emit a JSON array: %v", err)
	}
	// "error" is omitempty, so a successful row legitimately lacks it — the
	// key list covers the always-present set.
	assertKeys(t, "le stop --json result", keysOf(t, results[0]), stopResultKeys)

	// …and the failure shape must carry it, or a script can't tell why.
	buf.Reset()
	failing := func(scan.Listener, intel.Profile) (string, error) {
		return "", errStopFailed
	}
	if err := stopMatchedJSON(&buf, []row{sampleRow("3000", 42, "node")}, failing); err == nil {
		t.Error("a failed stop must still return a non-nil error for the exit code")
	}
	if !strings.Contains(buf.String(), `"error"`) {
		t.Errorf("a failed stop result must include an error key: %s", buf.String())
	}
}

// An empty result set must serialize as [] and never null: `le list nomatch
// --json | jq '.[]'` has to keep working.
func TestEmptyJSONIsArrayNotNull(t *testing.T) {
	var buf bytes.Buffer
	if err := printJSON(&buf, filterRows([]row{sampleRow("3000", 42, "node")}, "nomatch")); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("empty le list --json = %q, want []", got)
	}
}
