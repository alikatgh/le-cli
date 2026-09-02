//go:build windows

package scan

import (
	"fmt"
	"os/exec"
)

// The Windows backend: netstat enumerates listening sockets with their PIDs,
// and one PowerShell query over Win32_Process supplies name, command line,
// start time, owner and resource usage. Parsing lives in netstat.go, untagged,
// so it is tested on the machines this is developed on; this file is only the
// spawning.
//
// No LC_ALL here — the tools it runs are not locale-shaped the way ps is, and
// parseNetstat is written to be locale-proof on its own.

// runCmd runs a subprocess and returns its stdout. A package var for the same
// reason as the unix one: tests substitute canned output.
var runCmd = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// scanPlatform returns every TCP listener netstat can see. Cwd is left empty:
// Windows has no cheap, unprivileged way to read another process's working
// directory (it lives in the PEB, behind NtQueryInformationProcess and
// ReadProcessMemory), so the DIR column is honest-empty and `le stop --dir`
// has nothing to match against here. Documented in docs/COMPATIBILITY.md.
func scanPlatform() ([]Listener, error) {
	out, err := runCmd("netstat", "-ano")
	if err != nil {
		// netstat exits zero even with nothing listening, so any error means
		// it could not run — surface that rather than rendering an empty table
		// that is indistinguishable from "nothing is listening". (LE-CLI-002)
		return nil, fmt.Errorf("netstat: %w", err)
	}
	byPID, order := parseNetstat(out)
	if len(order) == 0 {
		return nil, nil
	}

	procs := readWinProcs(order)

	listeners := make([]Listener, 0, len(order))
	for _, pid := range order {
		a := byPID[pid]
		if len(a.addrs) == 0 {
			continue
		}
		p := procs[pid]
		l := Listener{
			PID:         pid,
			Command:     p.name,
			CommandLine: p.command,
			Addrs:       dedup(a.addrs),
			Ports:       portsOf(a.addrs),
			StartTime:   p.start,
			User:        p.user,
			CPU:         p.cpu,
			RSS:         p.rss,
		}
		if l.Command == "" {
			l.Command = "pid " + fmt.Sprint(pid)
		}
		if l.CommandLine == "" {
			l.CommandLine = l.Command
		}
		listeners = append(listeners, l)
	}
	return listeners, nil
}

// readWinProcs runs the Win32_Process query for a set of PIDs. Best-effort,
// like readPS: a PID the query cannot describe still gets a row from netstat.
func readWinProcs(pids []int) map[int]psRow {
	out, _ := runCmd("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", winProcScript(pids))
	return parseWinProcs(out)
}
