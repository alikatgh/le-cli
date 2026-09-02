// Package scan enumerates local TCP listeners, mirroring the macOS app's
// LocalhostScanner. On macOS and Linux that is lsof + ps (scan_unix.go); on
// Windows it is netstat + a Win32_Process query (scan_windows.go). Both feed
// the same Listener, and the safety-critical field is the same on each: a
// start time the kill package re-reads from the SAME source before it
// signals anything, so a recycled PID can never receive a stop meant for the
// process that used to own it.
package scan

import (
	"sort"
	"strconv"
	"strings"
)

// CPU% thresholds for the "hot" resource signal, shared by the TUI, `le list`,
// and (kept numerically in sync) the macOS app. ps %cpu is a sustained average
// where 100% = one full core, so these are genuine "this has been eating the
// machine" levels — an 818% emulator lands deep in Hot.
const (
	CPUWarmPct = 50.0  // amber — half a core
	CPUHotPct  = 200.0 // red   — two cores sustained
)

// Listener is one process holding one or more localhost ports.
type Listener struct {
	PID         int      `json:"pid"`
	Command     string   `json:"command"`     // short name from lsof (c field)
	CommandLine string   `json:"commandLine"` // full argv from ps
	User        string   `json:"user"`
	StartTime   string   `json:"startTime"` // ps lstart — authoritative recycle key
	Cwd         string   `json:"cwd"`
	CPU         float64  `json:"cpu"`   // ps %cpu — sustained average, may exceed 100 on multicore
	RSS         int      `json:"rss"`   // resident set size in KB (ps rss)
	Addrs       []string `json:"addrs"` // 127.0.0.1:3000, *:5000, [::1]:8080
	Ports       []string `json:"ports"`
}

// Scan returns every TCP listener visible to the current user, sorted by
// first port then PID. The enumeration itself is per-platform (scanPlatform
// in scan_unix.go / scan_windows.go); the contract each must meet is written
// on Listener.
func Scan() ([]Listener, error) {
	listeners, err := scanPlatform()
	if err != nil {
		return nil, err
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

// isLocalEndpoint reports whether an lsof address is reachable from localhost:
// loopback (127.x, [::1]) or a wildcard bind (*, 0.0.0.0, [::]) — the latter
// covers loopback too. A bind to a specific non-loopback IP (a LAN address) is
// not a localhost listener and is filtered out. Mirrors the mac app's
// LocalhostScanner.isLocalEndpoint prefix-for-prefix. (LE-796/LE-779)
func isLocalEndpoint(addr string) bool {
	return strings.HasPrefix(addr, "127.") ||
		strings.HasPrefix(addr, "[::1]") ||
		strings.HasPrefix(addr, "localhost:") ||
		strings.HasPrefix(addr, "*:") ||
		strings.HasPrefix(addr, "0.0.0.0:") ||
		strings.HasPrefix(addr, "[::]:")
}

// psRow is what a platform's process query knows about one PID. The unix path
// fills it from three ps calls; the Windows path from one Win32_Process query.
type psRow struct {
	start   string
	user    string
	command string
	cpu     float64
	rss     int
	name    string // short name; Windows only (lsof supplies it separately on unix)
}

func portsOf(addrs []string) []string {
	seen := map[string]bool{}
	// Non-nil so Ports serializes as [] not null when a listener's address
	// line was missing/unparseable — keeps `le list --json` uniform across
	// rows for jq / JSON consumers.
	out := make([]string, 0, len(addrs))
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
	out := make([]string, 0, len(in)) // non-nil: Addrs serializes as [] not null
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
