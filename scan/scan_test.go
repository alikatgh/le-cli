package scan

import "testing"

// Regression test for the exact bug the audit found: a single-line
// "pid,lstart,user,command" parse silently shifted every field once the
// account name contained a space. Splitting user/command into their own
// ps calls (each with the free-form field last) fixes it — prove it here.
func TestParsePSUserLinesHandlesSpacedUsername(t *testing.T) {
	rows := map[int]psRow{}
	parsePSUserLines("1234 Mon Jun 23 14:00:00 2026 jane doe", rows)
	if rows[1234].user != "jane doe" {
		t.Errorf("user = %q, want %q", rows[1234].user, "jane doe")
	}
	if rows[1234].start != "Mon Jun 23 14:00:00 2026" {
		t.Errorf("start = %q, want %q", rows[1234].start, "Mon Jun 23 14:00:00 2026")
	}
}

func TestParsePSUserLinesPlainUsername(t *testing.T) {
	rows := map[int]psRow{}
	parsePSUserLines("999 Sun Jun 28 09:58:51 2026 root", rows)
	if rows[999].user != "root" {
		t.Errorf("user = %q, want root", rows[999].user)
	}
}

func TestParsePSUserLinesSkipsShortLines(t *testing.T) {
	rows := map[int]psRow{}
	parsePSUserLines("garbage line with too few tokens", rows)
	if len(rows) != 0 {
		t.Errorf("expected no rows from a malformed line, got %v", rows)
	}
}

func TestParsePSCommandLinesHandlesSpaces(t *testing.T) {
	rows := map[int]psRow{}
	parsePSCommandLines("1234 /usr/bin/node app.js --port 3000", rows)
	if rows[1234].command != "/usr/bin/node app.js --port 3000" {
		t.Errorf("command = %q", rows[1234].command)
	}
}

func TestParsePSCommandLinesEmptyCommand(t *testing.T) {
	rows := map[int]psRow{}
	parsePSCommandLines("5555", rows) // a process ps couldn't fully describe
	if _, ok := rows[5555]; !ok {
		t.Error("pid should still be recorded even with an empty command")
	}
}

func TestReadPSMergesBothCalls(t *testing.T) {
	// Simulates readPS's merge behavior directly, without shelling out.
	rows := map[int]psRow{}
	parsePSUserLines("42 Mon Jun 23 14:00:00 2026 jane doe", rows)
	parsePSCommandLines("42 /usr/bin/python3 -m http.server 8000", rows)
	got := rows[42]
	if got.user != "jane doe" || got.start != "Mon Jun 23 14:00:00 2026" ||
		got.command != "/usr/bin/python3 -m http.server 8000" {
		t.Errorf("merged row = %+v", got)
	}
}

func TestPortOfHandlesIPv6AndWildcards(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:3000":     "3000",
		"*:5000":             "5000",
		"[::1]:8080":         "8080",
		"[::]:9000":          "9000",
		"[fe80::1%en0]:8080": "8080",
	}
	for addr, want := range cases {
		if got := portOf(addr); got != want {
			t.Errorf("portOf(%q) = %q, want %q", addr, got, want)
		}
	}
}
