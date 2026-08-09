package scan

import (
	"strings"
	"unicode/utf8"
)

// Non-ASCII process names come back mangled, and the cause is our own doing:
// cLocaleEnv forces LC_ALL=C (see scan.go — the lstart parse depends on it),
// and under the C locale both tools escape every byte >= 0x80 rather than
// passing it through. They do it in two different notations:
//
//	ps    /Applications/M-dM-<M^AM-dM-8M^ZM-eM->M-.M-dM-?M-!.app/…
//	lsof  c\xe4\xbc\x81\xe4\xb8\x9a\xe5\xbe\xae\xe4\xbf\xa1
//
// Both are the same UTF-8 bytes wearing different costumes (企业微信). Decoding
// here — rather than dropping LC_ALL=C — keeps the safety-critical parse
// deterministic while giving every consumer (TUI, `le list`, --json) the real
// name. A Chinese, Cyrillic, or emoji-named app is not an edge case; it's
// someone's daily driver, and "M-dM-<M^A" is unusable in every one of them.
//
// SAFETY: both decoders only commit when the result is valid UTF-8 that
// actually gained a multi-byte rune. A command line containing a literal
// "M-d" or "\x41" decodes to something invalid (or to nothing new) and is
// returned untouched, so ordinary ASCII argv can never be corrupted.

// unescapePS decodes ps's vis(3)-style notation:
//
//	M-<c>   byte c|0x80          (0xE4 → "M-d")
//	M^<c>   byte (c&0x1f)|0x80   (0x81 → "M^A") — note: NO dash before the
//	                              caret, which is what ps actually emits
//	M-^<c>  same, dashed form, accepted defensively
//	^<c>    control byte c&0x1f
func unescapePS(s string) string {
	if !strings.Contains(s, "M") && !strings.Contains(s, "^") {
		return s
	}
	var out []byte
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "M-^") && i+3 < len(s):
			out = append(out, (s[i+3]&0x1f)|0x80)
			i += 4
		case strings.HasPrefix(s[i:], "M^") && i+2 < len(s):
			out = append(out, (s[i+2]&0x1f)|0x80)
			i += 3
		case strings.HasPrefix(s[i:], "M-") && i+2 < len(s):
			out = append(out, s[i+2]|0x80)
			i += 3
		case s[i] == '^' && i+1 < len(s) && s[i+1] != ' ':
			out = append(out, s[i+1]&0x1f)
			i += 2
		default:
			out = append(out, s[i])
			i++
		}
	}
	return commitIfDecoded(s, string(out))
}

// unescapeLsof decodes lsof's `\xHH` byte escapes.
func unescapeLsof(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var out []byte
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], `\x`) && i+3 < len(s) {
			hi, okHi := hexVal(s[i+2])
			lo, okLo := hexVal(s[i+3])
			if okHi && okLo {
				out = append(out, hi<<4|lo)
				i += 4
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return commitIfDecoded(s, string(out))
}

func hexVal(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// commitIfDecoded accepts the decoded form only when it is valid UTF-8 that
// gained at least one multi-byte rune — the signature of a genuinely escaped
// name. Anything else (a literal "M-d" in an argument, a stray caret) fails
// one of those tests and the original survives untouched.
func commitIfDecoded(orig, decoded string) string {
	if decoded == orig || !utf8.ValidString(decoded) {
		return orig
	}
	if utf8.RuneCountInString(decoded) == len(decoded) {
		return orig // pure ASCII: nothing was actually un-escaped
	}
	return decoded
}
