package scan

import (
	"net"
	"os"
	"strconv"
	"testing"
)

// The one test that runs the REAL platform backend end to end: open a
// listener, scan, and find ourselves. Untagged on purpose — it is the same
// contract on every platform, so it runs against lsof+ps on macOS/Linux and
// against netstat+Win32_Process on CI's windows-latest, which is the only
// place the Windows glue is ever exercised for real.
//
// The assertions are the ones kill depends on: the PID must be found on the
// port we hold, and it must carry a non-empty StartTime — the recycle key —
// and a command line. A backend that returned rows without a start time would
// silently steer every stop onto stillSame's weaker command-line fallback.
func TestScanFindsThisProcessOnThePortItHolds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	listeners, err := Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, l := range listeners {
		if l.PID != os.Getpid() {
			continue
		}
		for _, p := range l.Ports {
			if p != port {
				continue
			}
			if l.StartTime == "" {
				t.Errorf("own listener has no StartTime — the PID-recycle guard would be running blind: %+v", l)
			}
			if l.CommandLine == "" {
				t.Errorf("own listener has no CommandLine: %+v", l)
			}
			return
		}
	}
	t.Fatalf("did not find pid %d on port %s among %d listeners", os.Getpid(), port, len(listeners))
}
