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

func runCmd(name string, args ...string) (string, error) {
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
	psInfo := readPS(csv)
	cwds := readCwd(csv)

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

// readPS pulls full command line, start time, and user for a set of PIDs in
// one call. lstart is always exactly 5 whitespace tokens
// ("Mon Jun 23 14:00:00 2026"), which makes the trailing command field
// (itself full of spaces) safe to parse.
func readPS(csv string) map[int]psRow {
	rows := map[int]psRow{}
	out, err := runCmd("ps", "-ww", "-p", csv, "-o", "pid=,lstart=,user=,command=")
	if err != nil && out == "" {
		return rows
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		rows[pid] = psRow{
			start:   strings.Join(fields[1:6], " "),
			user:    fields[6],
			command: strings.Join(fields[7:], " "),
		}
	}
	return rows
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

func atoi(s string) int        { n, _ := strconv.Atoi(s); return n }
func firstPortNum(p []string) int {
	if len(p) == 0 {
		return 1 << 30
	}
	return atoi(p[0])
}
