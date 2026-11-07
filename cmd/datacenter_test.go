package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func runDataCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r := newRoot("test", false)
	var out, diagnostics bytes.Buffer
	r.SetOut(&out)
	r.SetErr(&diagnostics)
	r.SetIn(strings.NewReader(""))
	r.SetArgs(args)
	err := r.Execute()
	return out.String(), err
}
func TestDatacenterOfflineCommands(t *testing.T) {
	// Invalid ambient URLs/tokens must be irrelevant in demo mode.
	t.Setenv("LOCALHOST_PROMETHEUS_URL", "https://must-not-contact.invalid")
	t.Setenv("LOCALHOST_NETBOX_TOKEN", "fixture-secret")
	for _, view := range []string{"summary", "fleet", "gpu", "inventory"} {
		t.Run(view, func(t *testing.T) {
			s, err := runDataCommand(t, "datacenter", view, "--demo", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]any
			if json.Unmarshal([]byte(s), &data) != nil || data["data_mode"] != "demo" || strings.Contains(s, "fixture-secret") {
				t.Fatalf("bad demo %s", s)
			}
		})
	}
	s, err := runDataCommand(t, "datacenter", "fleet", "--demo", "--host", "absent", "--json")
	if err != nil || !strings.Contains(s, `"rows": []`) {
		t.Fatalf("filter: %s %v", s, err)
	}
	s, err = runDataCommand(t, "datacenter", "inventory", "--demo", "--json")
	if err != nil || strings.Contains(s, `"name"`) {
		t.Fatalf("aggregate privacy: %s %v", s, err)
	}
	s, err = runDataCommand(t, "datacenter", "inventory", "--demo", "--details", "--json")
	if err != nil || !strings.Contains(s, `"name"`) {
		t.Fatalf("explicit details: %s %v", s, err)
	}
}
func TestDatacenterUsageNoOutput(t *testing.T) {
	t.Setenv("LOCALHOST_PROMETHEUS_URL", "")
	t.Setenv("LOCALHOST_NETBOX_URL", "")
	for _, args := range [][]string{{"fleet"}, {"fleet", "--demo", "--host", "bad\x1b"}, {"summary", "--demo", "--prometheus", "https://example.invalid"}, {"gpu", "--demo", "--timeout", "31s"}} {
		s, err := runDataCommand(t, append([]string{"datacenter"}, args...)...)
		if exitCodeFor(err) != 2 || s != "" {
			t.Fatalf("args %v output %q err %v", args, s, err)
		}
	}
}
func TestWriteDataSupplementaryFormatControls(t *testing.T) {
	c := &cobra.Command{}
	var out bytes.Buffer
	c.SetOut(&out)
	want := "a\U000e0001\u202eb"
	if err := writeData(c, map[string]string{"value": want}, true, func(io.Writer) {}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), '\U000e0001') || strings.ContainsRune(out.String(), '\u202e') {
		t.Fatal("literal format control in output")
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got["value"] != want {
		t.Fatalf("JSON semantics changed: %q %v", out.String(), err)
	}
	c.SetOut(shortDataWriter{})
	if !errors.Is(writeData(c, 1, true, nil), io.ErrShortWrite) {
		t.Fatal("short write not reported")
	}
}

type shortDataWriter struct{}

func (shortDataWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestDatabaseCommandDemoAndUsage(t *testing.T) {
	t.Setenv("LOCALHOST_DB_ADDRESS", "must-not-contact.invalid:1")
	t.Setenv("LOCALHOST_DB_PASSWORD", "fixture-secret")
	for _, engine := range []string{"postgres", "mysql", "mariadb", "mongodb", "redis"} {
		for _, action := range []string{"query", "schema"} {
			s, err := runDataCommand(t, "db", action, engine, "--demo", "--json")
			if err != nil || !strings.Contains(s, `"data_mode": "demo"`) || strings.Contains(s, "fixture-secret") {
				t.Fatalf("%s %s: %s %v", engine, action, s, err)
			}
		}
	}
	for _, args := range [][]string{{"query", "unknown", "--demo"}, {"schema", "redis", "--demo", "--address", "127.0.0.1:1"}, {"query", "postgres", "--demo", "--timeout", "11s"}, {"backup", "redis", "--demo"}, {"query", "postgres"}} {
		s, err := runDataCommand(t, append([]string{"db"}, args...)...)
		if exitCodeFor(err) != 2 || s != "" {
			t.Fatalf("args %v output %q err %v", args, s, err)
		}
	}
}
