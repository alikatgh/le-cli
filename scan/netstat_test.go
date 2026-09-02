package scan

import (
	"reflect"
	"strings"
	"testing"
)

// Captured from a real `netstat -ano`, plus the shapes the parser must reject:
// an ESTABLISHED row on a listening port's PID, UDP, a LAN-bound listener, and
// a header. The German row has no LISTENING token at all — it must be picked
// up from its zero foreign address alone, or every non-English user gets an
// empty table.
const netstatSample = `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1234
  TCP    0.0.0.0:445            0.0.0.0:0              LISTENING       4
  TCP    127.0.0.1:5000         0.0.0.0:0              LISTENING       9100
  TCP    127.0.0.1:5000         127.0.0.1:52113        ESTABLISHED     9100
  TCP    192.168.1.20:8080      0.0.0.0:0              LISTENING       7777
  TCP    [::]:135               [::]:0                 LISTENING       1234
  TCP    [::1]:8080             [::]:0                 ABHÖREN         4242
  UDP    0.0.0.0:5353           *:*                                    5678
`

func TestParseNetstatKeepsOnlyLocalTCPListeners(t *testing.T) {
	byPID, order := parseNetstat(netstatSample)

	if want := []int{1234, 4, 9100, 4242}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v (encounter order, LAN bind and UDP dropped)", order, want)
	}
	if got := byPID[1234].addrs; !reflect.DeepEqual(got, []string{"0.0.0.0:135", "[::]:135"}) {
		t.Errorf("pid 1234 addrs = %v: v4 and v6 binds of one process should merge", got)
	}
	if got := byPID[9100].addrs; !reflect.DeepEqual(got, []string{"127.0.0.1:5000"}) {
		t.Errorf("pid 9100 addrs = %v: the ESTABLISHED row must not add a second address", got)
	}
	if _, ok := byPID[7777]; ok {
		t.Error("a bind to a LAN IP is not a localhost listener and must be dropped")
	}
	if got := byPID[4242].addrs; !reflect.DeepEqual(got, []string{"[::1]:8080"}) {
		t.Errorf("localised State column: pid 4242 = %v, want found via zero foreign address", got)
	}
}

func TestParseNetstatEmptyAndGarbage(t *testing.T) {
	for _, in := range []string{"", "\n\n", "Active Connections\n  Proto  Local Address", "TCP nonsense"} {
		if _, order := parseNetstat(in); len(order) != 0 {
			t.Errorf("parseNetstat(%q) found %v, want nothing", in, order)
		}
	}
}

func TestWinProcScriptNamesEveryPID(t *testing.T) {
	s := winProcScript([]int{1234, 9100})
	for _, want := range []string{"ProcessId=1234 OR ProcessId=9100", "ToString('o')", "ConvertTo-Json -Compress -InputObject", "OutputEncoding"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
}

func TestParseWinProcsFillsRowsAndFallsBack(t *testing.T) {
	// A BOM and a null command line (a protected process) are both things the
	// real tool emits; a row with a zero PID is garbage and must be skipped.
	data := "\ufeff[" +
		`{"pid":9100,"name":"node.exe","cmd":"\"C:\\Program Files\\nodejs\\node.exe\" server.js","exe":"C:\\Program Files\\nodejs\\node.exe","start":"2026-08-30T15:13:51.1234567+03:00","user":"DESKTOP\\albert","cpu":12.5,"rss":56320},` +
		`{"pid":4,"name":"System","cmd":null,"exe":null,"start":"2026-08-30T09:00:00.0000000+03:00","user":"","cpu":0,"rss":100},` +
		`{"pid":0,"name":"System Idle Process","cmd":null,"exe":null,"start":"","user":"","cpu":0,"rss":8}` +
		"]"
	rows := parseWinProcs(data)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (pid 0 skipped): %+v", len(rows), rows)
	}
	n := rows[9100]
	if n.name != "node.exe" || n.start != "2026-08-30T15:13:51.1234567+03:00" || n.user != `DESKTOP\albert` || n.cpu != 12.5 || n.rss != 56320 {
		t.Errorf("node row = %+v", n)
	}
	if !strings.HasPrefix(n.command, `"C:\Program Files\nodejs\node.exe"`) {
		t.Errorf("command line should be the full argv, got %q", n.command)
	}
	if sys := rows[4]; sys.command != "System" {
		t.Errorf("null cmd and exe should fall back to the name, got %q", sys.command)
	}
}

func TestParseWinProcsAcceptsSingleObjectAndRejectsJunk(t *testing.T) {
	one := parseWinProcs(`{"pid":7,"name":"x.exe","start":"s"}`)
	if r, ok := one[7]; !ok || r.command != "x.exe" {
		t.Errorf("single-object document: %+v", one)
	}
	for _, junk := range []string{"", "null", "not json", "[{broken"} {
		if got := parseWinProcs(junk); len(got) != 0 {
			t.Errorf("parseWinProcs(%q) = %+v, want empty", junk, got)
		}
	}
}
