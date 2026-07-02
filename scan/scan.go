// Package scan enumerates local TCP listeners using lsof + ps, mirroring the
// macOS app's LocalhostScanner. lsof gives the listening sockets and each
// process's working directory; ps gives the full argv, start time, and user.
// Works on macOS and Linux (both ship lsof + ps).
package scan

import (
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Listener is one process holding one or more localhost ports.
type Listener struct {
	PID         int      `json:"pid"`
	Command     string   `json:"command"`     // short name from lsof (c field)
	CommandLine string   `json:"commandLine"` // full argv from ps
	User        string   `json:"user"`
	StartTime   string   `json:"startTime"` // ps lstart — authoritative recycle key
	Cwd         string   `json:"cwd"`
	Addrs       []string `json:"addrs"` // 127.0.0.1:3000, *:5000, [::1]:8080
	Ports       []string `json:"ports"`
}

// runCmd runs a subprocess and returns its stdout. It's a package var, not a
// plain func, so tests can substitute canned lsof/ps output and exercise the
// full Scan orchestration without shelling out. Overrides must stay
// goroutine-safe: readPS invokes this from two goroutines at once.
var runCmd = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// Scan returns every TCP listener visible to the current user.
func Scan() ([]Listener, error) {
	// -FpcnPT: field output keyed by p(pid) c(command) n(name/addr) plus protocol/state.
	out, _ := runCmd("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-FpcnPT")
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
				a.command = val
			}
		case 'n':
			if a := byPID[cur]; a != nil && val != "" {
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
		l := Listener{
			PID:       pid,
			Command:   a.command,
			Addrs:     dedup(a.addrs),
			Ports:     portsOf(a.addrs),
			Cwd:       cwds[pid],
			StartTime: psInfo[pid].start,
			User:      psInfo[pid].user,
		}
		l.CommandLine = psInfo[pid].command
		if l.CommandLine == "" {
			l.CommandLine = a.command
		}
		listeners = append(listeners, l)
	}

	sort.Slice(listeners, func(i, j int) bool {
		pi, pj := firstPortNum(listeners[i].Ports), firstPortNum(listeners[j].Ports)
		if pi != pj {
			return pi < pj
		}
		return listeners[i].PID < listeners[j].PID
	})
	return listeners, nil
}

type psRow struct {
	start   string
	user    string
	command string
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
	// The two ps calls are independent subprocess invocations — run them
	// concurrently. Each writes only to its own local `out` string during the
	// concurrent phase; the two parse passes below run after wg.Wait(), so
	// both write to the shared `rows` map only sequentially, never at once.
	var out1, out2 string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); out1, _ = runCmd("ps", "-ww", "-p", csv, "-o", "pid=,lstart=,user=") }()
	go func() { defer wg.Done(); out2, _ = runCmd("ps", "-ww", "-p", csv, "-o", "pid=,command=") }()
	wg.Wait()

	rows := map[int]psRow{}
	seenUser := parsePSUserLines(out1, rows)
	seenCommand := parsePSCommandLines(out2, rows)
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
			r.command = strings.Join(fields[1:], " ")
		}
		rows[pid] = r
		seen[pid] = true
	}
	return seen
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

func portsOf(addrs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range addrs {
		if p := portOf(a); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return atoi(out[i]) < atoi(out[j]) })
	return out
}

// portOf returns the port from an lsof address: 127.0.0.1:3000 -> 3000,
// *:5000 -> 5000, [::1]:8080 -> 8080.
func portOf(addr string) string {
	i := strings.LastIndex(addr, ":")
	if i < 0 || i+1 >= len(addr) {
		return ""
	}
	return addr[i+1:]
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
func firstPortNum(p []string) int {
	if len(p) == 0 {
		return 1 << 30
	}
	return atoi(p[0])
}
