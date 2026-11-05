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
	// #nosec G204 -- Private test seam: production callers select fixed brew,
	// launchctl or taskkill binaries; targets are discrete argv, no shell.
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C") // stable English errors, like runOutput (LE-394)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func execOutput(name string, args ...string) (string, error) {
	// #nosec G204 -- Production callers select fixed ps/powershell binaries.
	// ps receives numeric PID argv; PowerShell templates in taskkill.go interpolate
	// only strconv.Itoa(pid), never command text from a listener or caller.
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	return string(out), err
}

// Stop runs the recommended action for a listener. It returns a short result
// message, or an error if the PID was recycled or the action failed.
func Stop(l scan.Listener, p intel.Profile) (string, error) {
	if p.StopKind == intel.StopDocker || intel.IsDockerHelper(l) {
		return "", fmt.Errorf("%s", intel.DockerManualReason)
	}
	if !stillSame(l) {
		return "", fmt.Errorf("pid %d is gone or was recycled to a different process — rescan", l.PID)
	}
	switch p.StopKind {
	case intel.StopBrew:
		// A manual Cellar process may use the same formula as a different
		// registered service. Verify the selected PID belongs to the formula's
		// current user-domain launchd job before asking brew to act on it.
		if !intel.BrewServiceKnown(p.StopArg) {
			return "", fmt.Errorf("brew formula %q is no longer known to brew services — rescan and try again", p.StopArg)
		}
		if pid, ok := intel.LaunchdLabelPID("homebrew.mxcl." + p.StopArg); !launchdGuardOK(l.PID, pid, ok) {
			return "", fmt.Errorf("homebrew user service no longer owns the selected process — rescan and try again")
		}
		out, err := runCombined("brew", "services", "stop", p.StopArg)
		if err != nil {
			return "", cmdErr("brew services stop "+p.StopArg, out, err)
		}
		return "brew services stop " + p.StopArg, nil
	case intel.StopLaunchd:
		// stillSame confirms only the PID; it says nothing about whether the
		// label still maps to this
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

// Restart bounces a Homebrew-managed listener through its verified owner.
// Docker actions remain refused until local engine and container ownership can
// be established; a published-port match is insufficient.
func Restart(l scan.Listener, p intel.Profile) (string, error) {
	if p.StopKind == intel.StopDocker || intel.IsDockerHelper(l) {
		return "", fmt.Errorf("%s", intel.DockerManualReason)
	}
	if !stillSame(l) {
		return "", fmt.Errorf("pid %d is gone or was recycled to a different process — rescan", l.PID)
	}
	switch p.StopKind {
	case intel.StopBrew:
		if !intel.BrewServiceKnown(p.StopArg) {
			return "", fmt.Errorf("brew formula %q is no longer known to brew services — rescan and try again", p.StopArg)
		}
		if pid, ok := intel.LaunchdLabelPID("homebrew.mxcl." + p.StopArg); !launchdGuardOK(l.PID, pid, ok) {
			return "", fmt.Errorf("homebrew user service no longer owns the selected process — rescan and try again")
		}
		out, err := runCombined("brew", "services", "restart", p.StopArg)
		if err != nil {
			return "", cmdErr("brew services restart "+p.StopArg, out, err)
		}
		return "brew services restart " + p.StopArg, nil
	default:
		return "", fmt.Errorf("no safe restart for %s — only a verified Homebrew-managed listener can be bounced through its owner", p.Identity)
	}
}

// launchdGuardOK is the exact label-recycling comparison.
func launchdGuardOK(scanPID, curPID int, lookupOK bool) bool {
	return lookupOK && curPID == scanPID
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
