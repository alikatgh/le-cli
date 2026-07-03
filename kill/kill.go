// Package kill executes a listener's recommended stop action after
// re-verifying the PID hasn't been recycled — the same guard the macOS app's
// FolderKiller uses. A process's start time is the authoritative recycle key:
// a recycled PID necessarily has a later start time.
package kill

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// Command execution is indirected through package vars so tests can exercise
// Stop's strategy branches and stillSame's recycle guard without shelling out
// or signalling real processes. Production values just call the real tools.
// ps runs under LC_ALL=C so its lstart output is the stable English format
// scan.go captured under the same locale — otherwise stillSame would compare
// a C-locale re-read against a differently-localized capture and falsely
// report a recycled PID.
var (
	runCombined = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput()
		return string(out), err
	}
	runOutput = func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.Output()
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
			return "", cmdErr("brew services stop "+p.StopArg, out, err)
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
			return "", cmdErr("docker stop "+p.StopArg, out, err)
		}
		return "docker stop " + p.StopArg, nil
	case intel.StopLaunchd:
		// Same shape as the Docker guard: stillSame above only confirms the
		// PID; it says nothing about whether the label still maps to this
		// process. Labels can be bootout'd and re-bootstrapped onto a
		// different program between scan and stop.
		if pid, ok := intel.LaunchdLabelPID(p.StopArg); !launchdGuardOK(l.PID, pid, ok) {
			return "", fmt.Errorf("launchd label %q no longer maps to pid %d — rescan and try again", p.StopArg, l.PID)
		}
		target := intel.LaunchdDomainTarget(p.StopArg)
		out, err := runCombined("launchctl", "bootout", target)
		if err != nil {
			return "", cmdErr("launchctl bootout "+target, out, err)
		}
		return "launchctl bootout " + target, nil
	case intel.StopAvoid:
		return "", fmt.Errorf("no safe automatic stop — inspect this process first")
	default:
		if err := termProcess(l.PID); err != nil {
			return "", fmt.Errorf("kill -TERM %d: %w", l.PID, err)
		}
		return "sent SIGTERM to " + strconv.Itoa(l.PID), nil
	}
}

// launchdGuardOK is the label-recycling comparison, pure for the same reason
// as dockerGuardOK below: a refactor that inverts it silently defeats the
// protection, so give the tests something to pin.
func launchdGuardOK(scanPID, curPID int, lookupOK bool) bool {
	return lookupOK && curPID == scanPID
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
// is authoritative; if we never captured one, fall back to comparing the full
// command line, refusing anything short of an exact match (see below).
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
	// No start time — the command line is the only identity left. Require a
	// FULL match, not just the executable basename: when scan loses a PID's ps
	// row it falls back to lsof's short command NAME (e.g. "node"), which can't
	// tell two `node` servers apart, so a basename compare would happily
	// signal a recycled-to unrelated `node` process. Demanding a full-argv
	// match means that weak case refuses instead (a rescan recaptures the
	// start time and takes the strong path) — refusing beats a wrong SIGTERM.
	cur := normalizeWS(out)
	return cur != "" && cur == normalizeWS(l.CommandLine)
}

// normalizeWS collapses each run of whitespace to a single space and trims
// the ends, so two spellings of the same ps timestamp compare equal.
func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// cmdErr formats a stop failure, preferring the command's own stderr/stdout
// but falling back to the Go error when that output is empty — e.g. the tool
// wasn't found, so CombinedOutput's err is the only diagnostic and a bare
// "docker stop x: " would tell the user nothing.
func cmdErr(action string, out string, err error) error {
	msg := strings.TrimSpace(out)
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("%s: %s", action, msg)
}
