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
func AppName(cmdline string) string {
	if name := appBundleUnder(cmdline, "/Applications/"); name != "" {
		return name
	}
	if name := appBundleUnder(cmdline, "/System/Applications/"); name != "" {
		return name
	}
	if name := segmentAfter(cmdline, "/Application Support/"); name != "" {
		return name
	}
	// A bundle somewhere else entirely (~/Library/…, /opt/…): still better
	// than nothing, and still the product for anything self-contained.
	return bundleName(cmdline)
}

// appBundleUnder returns the bundle name when the command line's FIRST bundle
// sits directly under prefix — i.e. it is a top-level installed application.
func appBundleUnder(cmdline, prefix string) string {
	i := strings.Index(cmdline, prefix)
	if i < 0 {
		return ""
	}
	rest := cmdline[i+len(prefix):]
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
