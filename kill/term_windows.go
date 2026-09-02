//go:build windows

package kill

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Windows has no SIGTERM. `taskkill /PID n` is the closest thing to a
// graceful stop — it posts WM_CLOSE to the process's windows — and for a
// console server with no window it simply fails, telling you the process
// "can only be terminated forcefully". Escalating to /F automatically would
// be the wrong call for a tool whose whole promise is "TERM, never kill -9 by
// default": /F is a hard kill with no chance to clean up. So le refuses and
// hands over the exact command, the same way it refuses a launchd daemon and
// tells you what to inspect. termProcess is a var so the test-binary guard
// can neuter it.
var termProcess = func(pid int) error {
	out, err := runCombined("taskkill", "/PID", strconv.Itoa(pid))
	if err == nil {
		return nil
	}
	if taskkillRefused(out) {
		return fmt.Errorf("pid %d has no window to close gracefully — Windows can only end it forcefully. If you are sure: taskkill /F /PID %d", pid, pid)
	}
	// Detection above keys on taskkill's English text; a localised Windows
	// still lands here with taskkill's own (translated) reason, which is
	// still a refusal — the safety property holds, only the hint degrades.
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(msg)
}

func termCommand(pid int) string { return "taskkill /PID " + strconv.Itoa(pid) }
func termResult(pid int) string {
	return "asked pid " + strconv.Itoa(pid) + " to close (taskkill /PID " + strconv.Itoa(pid) + ")"
}

// The identity re-reads behind stillSame, from the SAME Win32_Process fields
// scan captured — CreationDate rendered with ToString('o'), and the same
// cmd → exe → name fallback for the command line — so a live process
// compares byte-for-byte equal and a recycled PID cannot.
func readStartTime(pid int) (string, error) {
	return runOutput("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", winStartScript(pid))
}

func readCommandLine(pid int) (string, error) {
	return runOutput("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", winCmdScript(pid))
}
