//go:build !windows

package scan

import "testing"

// These three parse real subprocess stdout (ps, and lsof indirectly via
// portOf) that this process doesn't control — a malformed or adversarial
// line must never panic le itself. We've already found two real parsing
// bugs here by hand this session (a spaced-username field shift, and a
// seen-vs-legitimately-empty conflation); fuzzing systematizes that hunt
// instead of relying on catching the next one by inspection.

func FuzzParsePSUserLines(f *testing.F) {
	f.Add("1234 Mon Jun 23 14:00:00 2026 jane doe")
	f.Add("999 Sun Jun 28 09:58:51 2026 root")
	f.Add("")
	f.Add("garbage line with too few tokens")
	f.Add("-1 Mon Jun 23 14:00:00 2026 root")
	f.Add("1234 Mon Jun 23 14:00:00 2026 \x00\xff weird bytes")
	f.Add("1234 Mon Jun 23 14:00:00 2026 日本語 ユーザー")
	f.Fuzz(func(t *testing.T, out string) {
		rows := map[int]psRow{}
		parsePSUserLines(out, rows)
	})
}

func FuzzParsePSCommandLines(f *testing.F) {
	f.Add("1234 /usr/bin/node app.js --port 3000")
	f.Add("5555")
	f.Add("")
	f.Add("not-a-pid command here")
	f.Add("1234 \x00\xff\x00 command with weird bytes")
	f.Fuzz(func(t *testing.T, out string) {
		rows := map[int]psRow{}
		parsePSCommandLines(out, rows)
	})
}

func FuzzPortOf(f *testing.F) {
	f.Add("127.0.0.1:3000")
	f.Add("*:5000")
	f.Add("[::1]:8080")
	f.Add("[fe80::1%en0]:8080")
	f.Add("")
	f.Add(":")
	f.Add("no-colon-here")
	f.Add("::::::")
	f.Fuzz(func(t *testing.T, addr string) {
		portOf(addr)
	})
}
