package kill

import (
	"strconv"
	"strings"
)

// Pure helpers for the Windows stop path, untagged so they are tested on the
// macOS/Linux machines this is developed on (see scan/netstat.go for the same
// arrangement and the reasoning).

// taskkillRefused reports whether taskkill's output is the "no window, would
// need /F" refusal rather than some other failure:
//
//	ERROR: The process with PID 1234 could not be terminated.
//	Reason: This process can only be terminated forcefully (with /F option).
//
// English-only by nature; a localised message is treated as a generic
// failure, which still refuses — see termProcess in term_windows.go.
func taskkillRefused(out string) bool {
	l := strings.ToLower(out)
	return strings.Contains(l, "forcefully") || strings.Contains(l, "/f option")
}

// winStartScript re-reads one PID's CreationDate with exactly the expression
// scan's winProcScript used, so the two strings are comparable verbatim.
func winStartScript(pid int) string {
	return "[Console]::OutputEncoding=[Text.Encoding]::UTF8; " +
		"$p=Get-CimInstance Win32_Process -Filter 'ProcessId=" + strconv.Itoa(pid) + "'; " +
		"if ($p -and $p.CreationDate) { $p.CreationDate.ToString('o') }"
}

// winCmdScript re-reads one PID's command line with scan's fallback chain
// (CommandLine, then ExecutablePath, then Name) so the no-start-time path of
// stillSame compares like against like.
func winCmdScript(pid int) string {
	return "[Console]::OutputEncoding=[Text.Encoding]::UTF8; " +
		"$p=Get-CimInstance Win32_Process -Filter 'ProcessId=" + strconv.Itoa(pid) + "'; " +
		"if ($p) { if ($p.CommandLine) { $p.CommandLine } elseif ($p.ExecutablePath) { $p.ExecutablePath } else { $p.Name } }"
}
