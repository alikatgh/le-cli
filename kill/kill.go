// Package kill executes a listener's recommended stop action after
// re-verifying the PID hasn't been recycled — the same guard the macOS app's
// FolderKiller uses. A process's start time is the authoritative recycle key:
// a recycled PID necessarily has a later start time.
package kill

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// Command execution is indirected through package vars so tests can exercise
// Stop's strategy branches and stillSame's recycle guard without shelling out
// or signalling real processes. Production values just call the real tools.
var (
	runCombined = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		return string(out), err
	}
	runOutput = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).Output()
		return string(out), err
	}
	termProcess = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
)

// Stop runs the recommended action for a listener. It returns a short result
// message, or an error if the PID was recycled or the action failed.
func Stop(l scan.Listener, p intel.Profile) (string, error) {
	if !stillSame(l) {
		return "", fmt.Errorf("pid %d is gone or was recycled to a different process — rescan", l.PID)
	}
	switch p.StopKind {
	case intel.StopBrew:
		// Formula names aren't reassignable to a different service the way
		// container names are, so this only catches "already uninstalled/
		// removed between scan and stop" — a narrower check than Docker's,
		// intentionally, not an oversight. See intel.BrewServiceKnown.
		if !intel.BrewServiceKnown(p.StopArg) {
			return "", fmt.Errorf("brew formula %q is no longer known to brew services — rescan and try again", p.StopArg)
		}
		out, err := runCombined("brew", "services", "stop", p.StopArg)
		if err != nil {
			return "", fmt.Errorf("brew services stop %s: %s", p.StopArg, strings.TrimSpace(out))
		}
		return "brew services stop " + p.StopArg, nil
	case intel.StopDocker:
		// The PID guard above only confirms the scanned PID (often a Docker
		// helper process, not the container itself) hasn't been recycled —
		// it says nothing about whether p.StopArg still names the same
		// container. Container names, unlike PIDs, can be freed and
		// reassigned, so re-check the ID immediately before acting.
		var curID string
		var lookupOK bool
		if p.StopArgID != "" {
			curID, lookupOK = intel.DockerContainerID(p.StopArg)
		}
		if !dockerGuardOK(p.StopArgID, curID, lookupOK) {
			return "", fmt.Errorf("container %q changed since scan — rescan and try again", p.StopArg)
		}
		out, err := runCombined("docker", "stop", p.StopArg)
		if err != nil {
			return "", fmt.Errorf("docker stop %s: %s", p.StopArg, strings.TrimSpace(out))
		}
		return "docker stop " + p.StopArg, nil
	case intel.StopAvoid:
		return "", fmt.Errorf("no safe automatic stop — inspect this process first")
	default:
		if err := termProcess(l.PID); err != nil {
			return "", fmt.Errorf("kill -TERM %d: %w", l.PID, err)
		}
		return "sent SIGTERM to " + strconv.Itoa(l.PID), nil
	}
}

// dockerGuardOK is the exact comparison the container-recycling guard rests
// on, pulled out as a pure function so a future refactor that inverts it
// (silently defeating the protection this exists to provide) has something to
// catch it.
func dockerGuardOK(scanArgID, curID string, lookupOK bool) bool {
	if scanArgID == "" {
		return true // nothing was captured at scan time to compare against
	}
	return lookupOK && curID == scanArgID
}

// stillSame confirms the live PID is still the process we scanned. Start time
// is authoritative; if we never captured one, fall back to comparing the
// executable basename so we don't refuse a legitimate stop.
func stillSame(l scan.Listener) bool {
	if l.StartTime != "" {
		out, err := runOutput("ps", "-p", strconv.Itoa(l.PID), "-o", "lstart=")
		if err != nil {
			return false
		}
		// Normalize whitespace on BOTH sides before comparing. `ps` lstart
		// pads single-digit days with a second space ("Jul  2" vs "Jul 12"),
		// and scan captures the start time via strings.Fields (which collapses
		// that) while this fresh read only TrimSpaces it — so on days 1-9 the
		// two spellings never matched and every stop was falsely refused as a
		// recycled PID. Collapse runs of whitespace the same way here.
		return normalizeWS(out) == normalizeWS(l.StartTime)
	}
	out, err := runOutput("ps", "-ww", "-p", strconv.Itoa(l.PID), "-o", "command=")
	if err != nil {
		return false
	}
	cur := strings.TrimSpace(out)
	return cur != "" && sameExe(cur, l.CommandLine)
}

// normalizeWS collapses each run of whitespace to a single space and trims
// the ends, so two spellings of the same ps timestamp compare equal.
func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func sameExe(a, b string) bool {
	return argv0Base(a) == argv0Base(b)
}

func argv0Base(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}
