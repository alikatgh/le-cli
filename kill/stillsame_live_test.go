package kill

import (
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

// The PID-recycle guard against a REAL process: scan ourselves, then confirm
// stillSame agrees we are still us. Untagged so it exercises ps on macOS/
// Linux and the Win32_Process re-read on CI's windows-latest — the only place
// the Windows re-read scripts are ever run for real. If the two sides ever
// rendered the start time differently (a locale, a padding, a ToString
// format), every stop on that platform would be refused as a recycled PID,
// and this is the test that would say so.
//
// It opts back into genuine execution for runOutput only: the re-reads are
// read-only queries. termProcess stays neutered by the test-binary guard, and
// nothing here reaches it.
func TestStillSameAgainstOurOwnLiveProcess(t *testing.T) {
	orig := runOutput
	runOutput = execOutput
	defer func() { runOutput = orig }()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	listeners, err := scan.Scan()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var self *scan.Listener
	for i := range listeners {
		if listeners[i].PID != os.Getpid() {
			continue
		}
		for _, p := range listeners[i].Ports {
			if p == port {
				self = &listeners[i]
			}
		}
	}
	if self == nil {
		t.Fatalf("scan did not find pid %d on port %s", os.Getpid(), port)
	}
	if self.StartTime == "" {
		t.Fatalf("scan gave no StartTime for our own process — the guard would run on its weak path: %+v", *self)
	}

	if !stillSame(*self) {
		t.Errorf("stillSame refused our own live process — start time re-read disagrees with scan's capture: %q", self.StartTime)
	}

	// A different start time for the same PID is precisely what a recycled
	// PID looks like. It must be refused.
	recycled := *self
	recycled.StartTime = "1999-01-01T00:00:00.0000000+00:00"
	if stillSame(recycled) {
		t.Error("stillSame accepted a PID whose recorded start time no longer matches — a recycled PID would get the signal")
	}

	// The weak path: no start time, command line is the only identity. Our
	// own captured command line must still match itself.
	weak := *self
	weak.StartTime = ""
	if !stillSame(weak) {
		t.Errorf("command-line fallback refused our own process: %q", self.CommandLine)
	}
}
