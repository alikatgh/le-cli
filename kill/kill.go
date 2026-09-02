// Package kill executes a listener's recommended stop action after
// re-verifying the PID hasn't been recycled — the same guard the macOS app's
// FolderKiller uses. A process's start time is the authoritative recycle key:
// a recycled PID necessarily has a later start time.
package kill

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
//
// The real implementations are named (execCombined, execOutput) rather than
// written inline so a test that needs genuine READ-ONLY execution — the live
// stillSame check against our own PID — can opt back into it, while the
// test-binary guard keeps termProcess neutered throughout.
//
// termProcess, and the two identity re-reads stillSame relies on, are the
// platform seam: term_unix.go signals with SIGTERM and re-reads via ps;
// term_windows.go uses taskkill and re-reads Win32_Process.
var (
	runCombined = execCombined
	runOutput   = execOutput
)

func execCombined(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C") // stable English errors, like runOutput (LE-394)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func execOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	return string(out), err
}

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
		// Stop by the immutable container ID, not the reassignable name. The
		// guard above confirmed name->ID at check time, but stopping by name
		// still leaves a TOCTOU window where a freed name could be grabbed by a
		// different container before this call lands. Fall back to the name only
		// when no ID was captured at scan time. Report the friendly name — it's
		// the container the user recognizes. Mirrors the mac app. (LE-060)
		target := p.StopArgID
		if target == "" {
			target = p.StopArg
		}
		out, err := runCombined("docker", "stop", target)
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
			return "", fmt.Errorf("%s: %w", termCommand(l.PID), err)
		}
		return termResult(l.PID), nil
	}
}

// Restart bounces a managed listener through its real owner — the CLI half of
// the app's Restart-listener tool. Only brew-managed and Docker listeners can
// be restarted (they have a supervisor to bounce them through); anything else
// has no clean restart, so we refuse rather than guess. Mirrors Stop's identity
// re-verification (recycled PID, changed container) before acting.
func Restart(l scan.Listener, p intel.Profile) (string, error) {
	if !stillSame(l) {
		return "", fmt.Errorf("pid %d is gone or was recycled to a different process — rescan", l.PID)
	}
	switch p.StopKind {
	case intel.StopBrew:
		if !intel.BrewServiceKnown(p.StopArg) {
			return "", fmt.Errorf("brew formula %q is no longer known to brew services — rescan and try again", p.StopArg)
		}
		out, err := runCombined("brew", "services", "restart", p.StopArg)
		if err != nil {
			return "", cmdErr("brew services restart "+p.StopArg, out, err)
		}
		return "brew services restart " + p.StopArg, nil
	case intel.StopDocker:
		var curID string
		var lookupOK bool
		if p.StopArgID != "" {
			curID, lookupOK = intel.DockerContainerID(p.StopArg)
		}
		if !dockerGuardOK(p.StopArgID, curID, lookupOK) {
			return "", fmt.Errorf("container %q changed since scan — rescan and try again", p.StopArg)
		}
		target := p.StopArgID
		if target == "" {
			target = p.StopArg
		}
		out, err := runCombined("docker", "restart", target)
		if err != nil {
			return "", cmdErr("docker restart "+p.StopArg, out, err)
		}
		return "docker restart " + p.StopArg, nil
	default:
		return "", fmt.Errorf("no safe restart for %s — only brew-managed or Docker listeners can be bounced through their owner", p.Identity)
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
		out, err := readStartTime(l.PID)
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
	out, err := readCommandLine(l.PID)
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
