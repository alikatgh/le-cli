package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// The bytes ARE the feature here. A typo in an escape sequence produces no
// error, no visible difference at the call site, and a command that silently
// fails to fix the thing it exists to fix — so they are asserted literally
// rather than by comparing against the same constant the code uses.
func TestFixTerminalWritesTheRecoverySequences(t *testing.T) {
	root := newRoot("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fix-terminal"})

	if err := root.Execute(); err != nil {
		t.Fatalf("fix-terminal returned %v, want nil", err)
	}

	got := out.String()
	for _, want := range []struct{ seq, why string }{
		{"\x1b[?1000l", "mouse press/release reporting"},
		{"\x1b[?1002l", "mouse cell-motion reporting — the mode le actually enables"},
		{"\x1b[?1003l", "mouse all-motion reporting"},
		{"\x1b[?1006l", "SGR mouse encoding"},
		{"\x1b[?1015l", "urxvt mouse encoding"},
		{"\x1b[?2004l", "bracketed paste"},
		{"\x1b[?25h", "cursor visibility — a hidden cursor is the other half of a wedged terminal"},
		{"\x1b[?1049l", "alt screen"},
	} {
		if !strings.Contains(got, want.seq) {
			t.Errorf("output missing %q (%s); got %q", want.seq, want.why, got)
		}
	}
}

// Nothing but escapes: a stray newline or a status line would be printed into
// whatever the user is looking at, and this command runs on a terminal that is
// already in a bad state.
func TestFixTerminalPrintsNothingButEscapes(t *testing.T) {
	root := newRoot("test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fix-terminal"})
	if err := root.Execute(); err != nil {
		t.Fatalf("fix-terminal returned %v, want nil", err)
	}

	// An exact comparison, not a per-chunk shape check. The shape check this
	// replaces accepted "\x1b[?1000lXh" — right prefix, right final byte, a
	// stray X printed into the user's terminal in between. The literal is
	// spelled out rather than taken from ui.ResetTerminal so the test still
	// fails if that constant is edited wrongly.
	want := "\x1b[?1006l\x1b[?1015l\x1b[?1003l\x1b[?1002l\x1b[?1000l" +
		"\x1b[?2004l\x1b[?25h\x1b[?1049l"
	if got := out.String(); got != want {
		t.Errorf("output = %q, want exactly the recovery sequences %q", got, want)
	}
}

// The point of the command is being findable by someone whose terminal is
// already broken; unregistered, it does not exist.
func TestFixTerminalIsRegisteredOnRoot(t *testing.T) {
	for _, c := range newRoot("test").Commands() {
		if strings.Fields(c.Use)[0] == "fix-terminal" {
			if c.Short == "" {
				t.Error("fix-terminal has no Short — it will be invisible in --help")
			}
			return
		}
	}
	t.Fatal("fix-terminal not registered on root")
}
