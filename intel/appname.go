package intel

import (
	"strings"

	"github.com/alikatgh/le-cli/scan"
)

// Naming the product behind a helper process.
//
// Eight rows on a real machine all read "App helper" while the answer sat in
// the command line the whole time: /Applications/OneDrive.app/Contents/…,
// /Applications/BlueStacks.app/…, /Applications/企业微信.app/…. A table that
// says "App helper" eight times has told you nothing; naming the app is the
// difference between a list of processes and a list of THINGS YOU RECOGNIZE.
//
// Two sources, in priority order, because they answer different questions:
//
//	/Applications/<Product>.app/…            → Product   (the bundle IS the product)
//	…/Application Support/<Vendor>/…         → Vendor    (the bundle is a sidecar)
//
// The vendor directory wins for a nested helper because it names the product
// rather than the helper: Figma's agent lives at
// …/Application Support/Figma/FigmaAgent.app/… and "Figma" beats "FigmaAgent".
// A bundle directly under /Applications is already the product, so it wins
// outright.

// AppName extracts the product name from a process's command line, or ""
// when the path reveals nothing (a bare /usr/libexec/rapportd, say).
//
// Only argv[0] is considered. Scanning the whole command line would let a
// bundle path in an ARGUMENT rename the process: `/usr/bin/tool --open
// /Applications/Other.app/…` is not Other, and naming it that — with raised
// confidence — is worse than the generic label it replaced.
func AppName(cmdline string) string {
	argv0 := executableArg(cmdline)
	if argv0 == "" {
		return ""
	}
	if name := appBundleUnder(argv0, "/Applications/"); name != "" {
		return name
	}
	if name := appBundleUnder(argv0, "/System/Applications/"); name != "" {
		return name
	}
	if name := segmentAfter(argv0, "/Application Support/"); name != "" {
		return name
	}
	// A bundle somewhere else entirely (~/Library/…, /opt/…): still better
	// than nothing, and still the product for anything self-contained.
	return bundleName(argv0)
}

// executableArg returns the argv[0] portion of a space-joined command line.
//
// argv boundaries are lost by the time ps hands us a single string, and a
// bundle path legitimately contains spaces ("Grammarly Desktop.app"), so the
// split can't be on whitespace. Instead it cuts at the first thing that can
// only be a NEW argument:
//
//	" -"  a flag                     (…/Electron --inspect …)
//	" /"  another absolute path      (/usr/local/bin/rsync /Users/…)
//
// Neither can occur inside a single path in practice — a space inside a path
// is followed by the rest of a component, not by a dash or a slash — while
// both reliably mark where argv[0] ended. Anything not starting with "/" is
// rejected outright. A path that does contain one of those sequences gets
// truncated and yields no name, which is the conservative direction: a
// generic label, never a confident wrong one.
func executableArg(cmdline string) string {
	cmdline = strings.TrimSpace(cmdline)
	if !strings.HasPrefix(cmdline, "/") {
		return ""
	}
	end := len(cmdline)
	for _, sep := range []string{" -", " /"} {
		if i := strings.Index(cmdline, sep); i >= 0 && i < end {
			end = i
		}
	}
	return cmdline[:end]
}

// appBundleUnder returns the bundle name when argv[0] IS a bundle directly
// under prefix — i.e. a top-level installed application. Anchored at the
// start, so the prefix has to be where the executable lives, not merely
// somewhere in the string.
func appBundleUnder(argv0, prefix string) string {
	if !strings.HasPrefix(argv0, prefix) {
		return ""
	}
	rest := argv0[len(prefix):]
	end := strings.Index(rest, ".app/")
	if end < 0 {
		return ""
	}
	name := rest[:end]
	// Directly under the prefix: no further separator before ".app".
	if strings.Contains(name, "/") {
		return ""
	}
	return name
}

// segmentAfter returns the single path segment following marker.
func segmentAfter(cmdline, marker string) string {
	i := strings.Index(cmdline, marker)
	if i < 0 {
		return ""
	}
	rest := cmdline[i+len(marker):]
	end := strings.Index(rest, "/")
	if end <= 0 {
		return ""
	}
	return rest[:end]
}

// bundleName returns the name of the first .app bundle anywhere in the path.
// Bounded by the preceding "/", so a bundle whose name contains spaces
// ("Grammarly Desktop.app") survives the fact that command lines are
// space-joined argv.
func bundleName(cmdline string) string {
	i := strings.Index(cmdline, ".app/")
	if i < 0 {
		return ""
	}
	head := cmdline[:i]
	if slash := strings.LastIndex(head, "/"); slash >= 0 {
		head = head[slash+1:]
	}
	if head == "" || strings.Contains(head, " -") {
		return ""
	}
	return head
}

// helperSuffix names the specific binary when it differs from the product, so
// the table can stay clean ("OneDrive") while the detail pane still answers
// "which of OneDrive's helpers is this?" — the information the old generic
// identity destroyed in both places at once.
func helperSuffix(l scan.Listener) string {
	name := strings.TrimSpace(l.Command)
	if name == "" || name == AppName(l.CommandLine) {
		return ""
	}
	return " Helper: " + name + "."
}
