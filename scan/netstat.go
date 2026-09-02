package scan

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The Windows backend's parsers. Deliberately NOT build-tagged: everything in
// this file is pure string handling, so it compiles and is tested on the
// macOS/Linux machines this project is developed on. Only the code that
// actually spawns netstat and PowerShell is tagged (scan_windows.go). That
// split is what makes the port developable without a Windows box — the
// parsing is verified locally against captured output, and CI's
// windows-latest job verifies the spawning against the real tools.

// netAcc mirrors the lsof accumulator in scan_unix.go: one process, its
// listening addresses in encounter order.
type netAcc struct {
	addrs []string
}

// parseNetstat extracts TCP listeners from `netstat -ano` output:
//
//	Proto  Local Address          Foreign Address        State           PID
//	TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1234
//	TCP    [::]:135               [::]:0                 LISTENING       1234
//	TCP    127.0.0.1:5000         127.0.0.1:52113        ESTABLISHED     4321
//	UDP    0.0.0.0:5353           *:*                                    5678
//
// A listening socket is recognised STRUCTURALLY — a TCP row whose foreign
// address is the zero endpoint (0.0.0.0:0 or [::]:0) — rather than by the
// State column. netstat localises that column ("ABHÖREN" on a German
// system), and a parser keyed on the English word would silently return an
// empty table for every non-English user. The zero foreign address is a
// protocol fact, not a translation. LISTENING is still accepted as a
// belt-and-braces match.
//
// Like the lsof path, only localhost-reachable binds are kept (loopback and
// wildcard); a bind to a specific LAN IP is not a localhost listener.
func parseNetstat(out string) (map[int]*netAcc, []int) {
	byPID := map[int]*netAcc{}
	var order []int
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.HasPrefix(f[0], "TCP") {
			continue
		}
		local, foreign, state, pidStr := f[1], f[2], f[3], f[4]
		listening := foreign == "0.0.0.0:0" || foreign == "[::]:0" || state == "LISTENING"
		if !listening || !isLocalEndpoint(local) {
			continue
		}
		pid, err := strconv.Atoi(pidStr)
		if err != nil || pid <= 0 {
			continue
		}
		a, ok := byPID[pid]
		if !ok {
			a = &netAcc{}
			byPID[pid] = a
			order = append(order, pid)
		}
		a.addrs = append(a.addrs, local)
	}
	return byPID, order
}

// winProc is one row of the JSON the PowerShell query in winProcScript emits.
type winProc struct {
	PID   int     `json:"pid"`
	Name  string  `json:"name"`
	Cmd   string  `json:"cmd"`
	Exe   string  `json:"exe"`
	Start string  `json:"start"`
	User  string  `json:"user"`
	CPU   float64 `json:"cpu"`
	RSS   int     `json:"rss"`
}

// winProcScript builds the PowerShell that describes a set of PIDs as one
// JSON array. One process spawn for the whole table — PowerShell's startup
// cost is paid once, not per PID.
//
// Field by field, and why:
//
//   - start: Win32_Process.CreationDate rendered with ToString('o') — ISO 8601
//     with full precision. This is the PID-recycle key. kill re-reads it with
//     the SAME expression (kill/term_windows.go) so the two strings compare
//     byte-for-byte; there is no locale or padding hazard of the kind the
//     ps lstart path had to normalise away (LE-CLI-00x "Jul  2").
//   - cpu: ps %cpu is CPU time divided by wall-clock age, so the same ratio
//     is computed here from UserModeTime+KernelModeTime (100ns units) over
//     the seconds since CreationDate. Same semantics, same thresholds.
//   - rss: WorkingSetSize in KB, matching ps rss.
//   - user: GetOwner, best-effort — access is routinely denied for another
//     user's or a system process, and an empty user is the honest answer.
//   - cmd may be null for protected processes; the Go side falls back to
//     exe, then name.
//
// [Console]::OutputEncoding is forced to UTF-8 first: Windows PowerShell
// otherwise writes a pipe in the console code page, and a non-ASCII product
// name would arrive mangled. -NoProfile keeps a user's profile script out of
// both the timing and the output.
func winProcScript(pids []int) string {
	clauses := make([]string, 0, len(pids))
	for _, p := range pids {
		clauses = append(clauses, "ProcessId="+strconv.Itoa(p))
	}
	filter := strings.Join(clauses, " OR ")
	return "[Console]::OutputEncoding=[Text.Encoding]::UTF8; " +
		"$now=Get-Date; " +
		"$rows=@(Get-CimInstance Win32_Process -Filter '" + filter + "' | ForEach-Object { " +
		"$o=$null; try { $o=Invoke-CimMethod -InputObject $_ -MethodName GetOwner -ErrorAction Stop } catch {}; " +
		"$cpu=0; $start=''; " +
		"if ($_.CreationDate) { $start=$_.CreationDate.ToString('o'); " +
		"$age=($now - $_.CreationDate).TotalSeconds; " +
		"if ($age -gt 0) { $cpu=[math]::Round(((($_.UserModeTime + $_.KernelModeTime) / 1e7) / $age) * 100, 1) } }; " +
		"[pscustomobject]@{ pid=$_.ProcessId; name=$_.Name; cmd=$_.CommandLine; exe=$_.ExecutablePath; " +
		"start=$start; user=$(if ($o -and $o.User) { \"$($o.Domain)\\$($o.User)\" } else { '' }); " +
		"cpu=$cpu; rss=[int]($_.WorkingSetSize/1KB) } }); " +
		"ConvertTo-Json -Compress -InputObject $rows"
}

// parseWinProcs decodes winProcScript's output into the same psRow shape the
// ps parsers produce, so Scan's assembly loop is identical on every platform.
// A single-object document (what an older ConvertTo-Json emits for one row
// without -InputObject) is accepted alongside the array. Any parse failure
// yields an empty map: the table still renders from netstat alone, with the
// short name missing — the same degradation the lsof path has when ps drops
// a PID.
func parseWinProcs(data string) map[int]psRow {
	rows := map[int]psRow{}
	data = strings.TrimSpace(strings.TrimPrefix(data, "\ufeff"))
	if data == "" || data == "null" {
		return rows
	}
	var procs []winProc
	if err := json.Unmarshal([]byte(data), &procs); err != nil {
		var one winProc
		if err := json.Unmarshal([]byte(data), &one); err != nil {
			return rows
		}
		procs = []winProc{one}
	}
	for _, p := range procs {
		if p.PID <= 0 {
			continue
		}
		command := p.Cmd
		if command == "" {
			command = p.Exe
		}
		if command == "" {
			command = p.Name
		}
		rows[p.PID] = psRow{
			start:   p.Start,
			user:    p.User,
			command: command,
			cpu:     p.CPU,
			rss:     p.RSS,
			name:    p.Name,
		}
	}
	return rows
}
