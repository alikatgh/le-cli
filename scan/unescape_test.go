//go:build !windows

package scan

import (
	"testing"
	"unicode/utf8"
)

// The real strings from a WeCom (企业微信) listener on macOS — the ones that
// rendered as line noise in the TUI detail pane and in `le list --json`.
const (
	realPSEscaped = `/Applications/M-dM-<M^AM-dM-8M^ZM-eM->M-.M-dM-?M-!.app/Contents/MacOS/M-dM-<M^AM-dM-8M^ZM-eM->M-.M-dM-?M-!`
	realPSDecoded = `/Applications/企业微信.app/Contents/MacOS/企业微信`
)

func TestUnescapePS(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"real WeCom path from ps", realPSEscaped, realPSDecoded},
		{"cyrillic", `M-QM^GM-QM^BM-PM->`, "что"},
		{"plain ascii untouched", "/usr/local/bin/node server.js", "/usr/local/bin/node server.js"},
		{"empty", "", ""},
		// The false-positive guard: these LOOK like escapes but don't decode to
		// valid multi-byte UTF-8, so the original must survive byte-for-byte.
		{"literal M- in an argument", "java -Dm=M-dfoo", "java -Dm=M-dfoo"},
		{"lone caret", "sh -c 'echo a^b'", "sh -c 'echo a^b'"},
		{"trailing M-", "weird M-", "weird M-"},
		{"caret at end", "weird ^", "weird ^"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unescapePS(c.in); got != c.want {
				t.Errorf("unescapePS(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestUnescapeLsof(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"real WeCom name from lsof", `\xe4\xbc\x81\xe4\xb8\x9a\xe5\xbe\xae\xe4\xbf\xa1`, "企业微信"},
		{"emoji", `my\xf0\x9f\x9a\x80app`, "my🚀app"},
		{"plain ascii untouched", "node", "node"},
		{"empty", "", ""},
		// Decodes to pure ASCII → nothing was really escaped → leave it alone,
		// so a literal backslash-x in a name is never silently rewritten.
		{"ascii hex escape left alone", `\x41\x42`, `\x41\x42`},
		{"invalid hex digits", `\xzz`, `\xzz`},
		{"truncated escape", `abc\x`, `abc\x`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unescapeLsof(c.in); got != c.want {
				t.Errorf("unescapeLsof(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// End-to-end through the parser Scan() actually uses, so the wiring is covered
// and not just the helper.
func TestParsePSCommandLinesDecodesNonASCII(t *testing.T) {
	rows := map[int]psRow{}
	parsePSCommandLines("27470 "+realPSEscaped, rows)
	if got := rows[27470].command; got != realPSDecoded {
		t.Errorf("command = %q, want %q", got, realPSDecoded)
	}
}

// Whatever a decoder does, it must never hand downstream code invalid UTF-8 —
// that corrupts the JSON output and the TUI's width calculations alike.
func FuzzUnescapeAlwaysValidUTF8(f *testing.F) {
	for _, s := range []string{realPSEscaped, `\xe4\xbc\x81`, "M-", "^", `\x`, "node -e 1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, got := range []string{unescapePS(s), unescapeLsof(s)} {
			// A decoder may return the input verbatim (that's the guard doing
			// its job), so only a CHANGED result has to be valid UTF-8.
			if got != s && !isValidUTF8(got) {
				t.Errorf("decoded %q into invalid UTF-8 %q", s, got)
			}
		}
	})
}

func isValidUTF8(s string) bool { return utf8.ValidString(s) }
