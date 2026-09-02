package kill

import (
	"strings"
	"testing"
)

func TestTaskkillRefusedRecognisesTheNoWindowCase(t *testing.T) {
	refusal := "ERROR: The process with PID 1234 could not be terminated.\r\n" +
		"Reason: This process can only be terminated forcefully (with /F option).\r\n"
	if !taskkillRefused(refusal) {
		t.Error("the canonical /F refusal must be recognised")
	}
	for _, other := range []string{
		"ERROR: The process \"1234\" not found.",
		"ERROR: Access is denied.",
		"",
		"FEHLER: Der Prozess konnte nicht beendet werden.", // localised: generic failure, still a refusal upstream
	} {
		if taskkillRefused(other) {
			t.Errorf("%q is not the /F refusal", other)
		}
	}
}

// The two re-read scripts must use the same expressions scan captured with,
// or a live process would compare unequal to itself and every stop on Windows
// would be refused as a recycled PID.
func TestWindowsRereadScriptsMatchScanCapture(t *testing.T) {
	start := winStartScript(4242)
	for _, want := range []string{"ProcessId=4242", "ToString('o')", "OutputEncoding"} {
		if !strings.Contains(start, want) {
			t.Errorf("start script lacks %q: %s", want, start)
		}
	}
	cmd := winCmdScript(4242)
	for _, want := range []string{"ProcessId=4242", "CommandLine", "ExecutablePath", "Name"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command script lacks %q: %s", want, cmd)
		}
	}
}
