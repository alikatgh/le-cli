//go:build !windows

package scan

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// The macOS / Linux backend: lsof enumerates listening sockets and each
// process's working directory; ps supplies argv, start time, user, and
// resource usage. Both ship on both platforms. The Windows backend lives in
// scan_windows.go and feeds the same Listener shape; everything they share
// (types, address filtering, port helpers, the final sort) is in scan.go.

// cLocaleEnv runs ps/lsof under LC_ALL=C so their output is the stable,
// English, fixed-shape format we parse. Without it, ps lstart is rendered
// via the caller's locale (LC_TIME) — e.g. ru_RU/ja_JP produce a different
// token count and word order, which broke the fixed-offset lstart parse and,
// downstream, the PID-recycle guard.
var cLocaleEnv = append(os.Environ(), "LC_ALL=C")

// runCmd runs a subprocess and returns its stdout. It's a package var, not a
// plain func, so tests can substitute canned lsof/ps output and exercise the
// full Scan orchestration without shelling out. Overrides must stay
// goroutine-safe: readPS invokes this from two goroutines at once.
var runCmd = func(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = cLocaleEnv
	out, err := cmd.Output()
	return string(out), err
}

// scanPlatform is the lsof + ps enumeration — the original Scan body,
// unchanged. Scan (scan.go) sorts what it returns.
func scanPlatform() ([]Listener, error) {
	// -FpcnPT: field output keyed by p(pid) c(command) n(name/addr) plus protocol/state.
	out, err := runCmd("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-FpcnPT")
	if err != nil {
		// lsof exits non-zero when nothing is listening, so a non-zero *exit*
		// is the normal empty case — not a failure. But if lsof couldn't run at
		// all (missing binary, unexecutable), that's a real error we must
		// surface: otherwise a broken environment is indistinguishable from
		// "nothing is listening", and `le stop` reports "nothing listening"
		// when the scan actually failed (LE-CLI-002 / LE-CLI-014). An
		// *exec.ExitError means lsof ran and exited non-zero; anything else
		// (e.g. *exec.Error wrapping ErrNotFound) means it never ran.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, fmt.Errorf("lsof: %w", err)
		}
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil // no listeners (lsof exits non-zero on empty match)
	}

	type acc struct {
		command string
		addrs   []string
	}
	byPID := map[int]*acc{}
	var order []int
	cur := -1

	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		tag, val := line[0], line[1:]
		switch tag {
		case 'p':
			pid, err := strconv.Atoi(val)
			if err != nil {
				continue
			}
			cur = pid
			if _, ok := byPID[pid]; !ok {
				byPID[pid] = &acc{}
				order = append(order, pid)
			}
		case 'c':
			if a := byPID[cur]; a != nil {
				// lsof escapes non-ASCII as \xHH under LC_ALL=C — decode it
				// (different notation from ps's, same cause). See unescape.go.
				a.command = unescapeLsof(val)
			}
		case 'n':
			// Only keep localhost-reachable binds (loopback + wildcards). A bind
			// to a specific LAN IP is not a localhost listener — parity with the
			// mac app's isLocalEndpoint, which filters the same way. (LE-796/LE-779)
			if a := byPID[cur]; a != nil && val != "" && isLocalEndpoint(val) {
				a.addrs = append(a.addrs, val)
			}
		}
	}

	if len(order) == 0 {
		return nil, nil
	}

	pids := make([]string, 0, len(order))
	for _, p := range order {
		pids = append(pids, strconv.Itoa(p))
	}
	csv := strings.Join(pids, ",")
	// readPS and readCwd are independent subprocess calls (ps vs lsof) with
	// no data dependency on each other — run them concurrently so their
	// blocking I/O waits overlap instead of summing.
	var psInfo map[int]psRow
	var cwds map[int]string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); psInfo = readPS(csv) }()
	go func() { defer wg.Done(); cwds = readCwd(csv) }()
	wg.Wait()

	listeners := make([]Listener, 0, len(order))
	for _, pid := range order {
		a := byPID[pid]
		// A PID whose only binds were non-localhost drops out entirely rather
		// than becoming a row with no address/port. (LE-796)
		if len(a.addrs) == 0 {
			continue
		}
		l := Listener{
			PID:       pid,
			Command:   a.command,
			Addrs:     dedup(a.addrs),
			Ports:     portsOf(a.addrs),
			Cwd:       cwds[pid],
			StartTime: psInfo[pid].start,
			User:      psInfo[pid].user,
			CPU:       psInfo[pid].cpu,
			RSS:       psInfo[pid].rss,
		}
		l.CommandLine = psInfo[pid].command
		if l.CommandLine == "" {
			l.CommandLine = a.command
		}
		listeners = append(listeners, l)
	}

	return listeners, nil
}

// readPS pulls start time, user, and full command line for a set of PIDs.
// Two separate ps calls, each with its free-form field last, rather than one
// "pid,lstart,user,command" line: lstart is a fixed 5 tokens, but user and
// command are both variable-length and can each contain spaces (a
// directory-joined account name; an argv with embedded spaces), so packing
// both into one line made a single unexpected space silently shift every
// field after it. Putting each variable field last in its own call makes it
// unambiguously "everything remaining on the line," however many words long.
func readPS(csv string) map[int]psRow {
	// The ps calls are independent subprocess invocations — run them
	// concurrently. Each writes only to its own local `out` string during the
	// concurrent phase; the parse passes below run after wg.Wait(), so they
	// write to the shared `rows` map only sequentially, never at once.
	//
	// out3 (%cpu, rss) is deliberately its OWN call rather than folded into
	// out1: out1's lstart feeds kill.Stop's PID-recycle guard — the safety-
	// critical path — and its parse depends on exact field offsets around the
	// 5-token lstart. Keeping cpu/rss (two fixed single-token numerics) in a
	// separate call leaves that parse byte-for-byte untouched. cpu/rss are
	// decorative, so a PID this call misses just gets 0 — it is NOT part of
	// dropPartialRows' all-or-nothing guarantee (unlike start time).
	var out1, out2, out3 string
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); out1, _ = runCmd("ps", "-ww", "-p", csv, "-o", "pid=,lstart=,user=") }()
	go func() { defer wg.Done(); out2, _ = runCmd("ps", "-ww", "-p", csv, "-o", "pid=,command=") }()
	go func() { defer wg.Done(); out3, _ = runCmd("ps", "-ww", "-p", csv, "-o", "pid=,%cpu=,rss=") }()
	wg.Wait()

	rows := map[int]psRow{}
	seenUser := parsePSUserLines(out1, rows)
	seenCommand := parsePSCommandLines(out2, rows)
	parsePSResourceLines(out3, rows) // best-effort; not gated by dropPartialRows
	dropPartialRows(rows, seenUser, seenCommand)
	return rows
}

// dropPartialRows enforces the old single-call code's all-or-nothing
// semantic. A PID captured by only ONE of the two ps calls (e.g. a transient
// per-call hiccup, not necessarily a PID recycle) would otherwise ship a row
// pairing a real CommandLine with an empty StartTime — which silently steers
// kill.Stop's stillSame() onto its weaker basename-only fallback for a
// listener that could have had the strong lstart-based guard if either call
// alone had succeeded fully. Dropping the row entirely (rather than keeping
// the half it did capture) restores the original "have everything or have
// nothing for this PID" guarantee; Scan() already has a fallback (lsof's
// short command name) for a PID readPS has no data for at all.
//
// seenUser/seenCommand record which PIDs each ps call actually produced a
// line for — membership, not field emptiness. A PID a call SAW but whose
// free-form field (user, or command) is legitimately empty is still
// "complete"; inferring completeness from the field's string value alone
// conflated that case with a PID a call never saw at all, and dropped a row
// it should have kept.
func dropPartialRows(rows map[int]psRow, seenUser, seenCommand map[int]bool) {
	for pid := range rows {
		if seenUser[pid] != seenCommand[pid] {
			delete(rows, pid)
		}
	}
}

func parsePSUserLines(out string, rows map[int]psRow) map[int]bool {
	seen := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 { // pid + 5 lstart tokens + at least one user token
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		r := rows[pid]
		r.start = strings.Join(fields[1:6], " ")
		r.user = strings.Join(fields[6:], " ")
		rows[pid] = r
		seen[pid] = true
	}
	return seen
}

func parsePSCommandLines(out string, rows map[int]psRow) map[int]bool {
	seen := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		r := rows[pid]
		if len(fields) > 1 {
			// LC_ALL=C makes ps vis-escape every non-ASCII byte; decode so a
			// Chinese/Cyrillic/emoji app name reads as itself. See unescape.go.
			r.command = unescapePS(strings.Join(fields[1:], " "))
		}
		rows[pid] = r
		seen[pid] = true
	}
	return seen
}

// parsePSResourceLines fills cpu (%) and rss (KB) from
// `ps -o pid=,%cpu=,rss=`. Both are fixed single-token numerics, so a plain
// field split is unambiguous — no lstart-style spacing hazard. Best-effort:
// a malformed or missing value leaves the row's cpu/rss at zero rather than
// dropping the row. Resource stats are decorative; unlike the start time
// (the recycle guard's authoritative key) they are NOT part of
// dropPartialRows' all-or-nothing guarantee, so this returns no seen-set.
func parsePSResourceLines(out string, rows map[int]psRow) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		r := rows[pid]
		r.cpu, _ = strconv.ParseFloat(fields[1], 64)
		r.rss, _ = strconv.Atoi(fields[2])
		rows[pid] = r
	}
}

// readCwd maps PID -> working directory via lsof's cwd file descriptor.
func readCwd(csv string) map[int]string {
	cwds := map[int]string{}
	out, _ := runCmd("lsof", "-a", "-d", "cwd", "-p", csv, "-Fpn")
	cur := -1
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		tag, val := line[0], line[1:]
		switch tag {
		case 'p':
			if pid, err := strconv.Atoi(val); err == nil {
				cur = pid
			}
		case 'n':
			if cur != -1 {
				cwds[cur] = val
			}
		}
	}
	return cwds
}
