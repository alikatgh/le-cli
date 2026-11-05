package intel

import (
	"fmt"
	"strings"

	"github.com/alikatgh/le-cli/scan"
)

// EvidenceStatus separates an OS observation from a heuristic explanation.
// These are provenance labels, never probabilities or permission to stop.
type EvidenceStatus string

const (
	Observed    EvidenceStatus = "observed"
	Inferred    EvidenceStatus = "inferred"
	Unavailable EvidenceStatus = "unavailable"
)

type EvidenceItem struct {
	Status EvidenceStatus `json:"status"`
	Detail string         `json:"detail"`
}

// Evidence is additive to the original profile contract. Resource fields are
// deliberately excluded: legacy collectors default CPU/RSS to zero without an
// availability flag, so zero cannot establish a successful measurement.
type Evidence struct {
	Listener       EvidenceItem `json:"listener"`
	Command        EvidenceItem `json:"command"`
	Directory      EvidenceItem `json:"directory"`
	StartTime      EvidenceItem `json:"startTime"`
	Classification EvidenceItem `json:"classification"`
	Owner          EvidenceItem `json:"owner"`
}

// HasCommandLine is conservative across collectors: Windows substitutes the
// short process name when argv is inaccessible. That fallback must never be
// described as an observed command line (or used to retain PID-only selection).
// A genuine name-only argv is indistinguishable and stays unavailable too.
func HasCommandLine(l scan.Listener) bool {
	command := strings.TrimSpace(l.CommandLine)
	return !l.CommandLineFallback && command != "" && command != strings.TrimSpace(l.Command)
}

func evidenceFor(l scan.Listener, p Profile, env Env) Evidence {
	e := Evidence{
		Listener:       EvidenceItem{Unavailable, "No listener endpoint recorded."},
		Command:        EvidenceItem{Unavailable, "Full command line unavailable; a process name may be shown instead."},
		Directory:      EvidenceItem{Unavailable, "Working directory unavailable."},
		StartTime:      EvidenceItem{Unavailable, "Process start time unavailable; PID identity cannot be verified."},
		Classification: EvidenceItem{Inferred, "Identity and stop advice use rules, not measured probabilities."},
		Owner:          EvidenceItem{Unavailable, "Process ownership not verified."},
	}
	if len(l.Addrs) > 0 || len(l.Ports) > 0 {
		e.Listener = EvidenceItem{Observed, "TCP listener reported by the OS listener scan."}
	}
	if HasCommandLine(l) {
		e.Command = EvidenceItem{Observed, "Command line reported by the OS process query."}
	}
	if strings.TrimSpace(l.Cwd) != "" {
		e.Directory = EvidenceItem{Observed, "Working directory reported by the OS file query."}
	}
	if strings.TrimSpace(l.StartTime) != "" {
		e.StartTime = EvidenceItem{Observed, "Start time reported by the OS; rechecked before stopping."}
	}
	switch {
	case p.Source == SrcContainer:
		e.Owner = EvidenceItem{Unavailable, DockerManualReason}
	case env.LaunchdByPID[l.PID] != "":
		e.Owner = EvidenceItem{Observed, fmt.Sprintf("launchd reports PID %d as %s.", l.PID, env.LaunchdByPID[l.PID])}
	case p.Source != "" && p.Source != SrcUnknown:
		e.Owner = EvidenceItem{Inferred, "Source inferred from command, path, and service context; process ownership unverified."}
	}
	return e
}

// Summary keeps all six evidence fields visible in a bounded detail pane.
func (e Evidence) Summary() string {
	status := func(s EvidenceStatus) string {
		if s == "" {
			return string(Unavailable)
		}
		return string(s)
	}
	return "listener " + status(e.Listener.Status) + " · cmd " + status(e.Command.Status) +
		" · dir " + status(e.Directory.Status) + "\nstart " + status(e.StartTime.Status) +
		" · class " + status(e.Classification.Status) + " · owner " + status(e.Owner.Status)
}
