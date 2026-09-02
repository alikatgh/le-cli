//go:build !darwin && !windows

package tools

// xdg-open is the freedesktop launcher every mainstream Linux desktop honours.
// (Before the platform split this path ran /usr/bin/open, which does not
// exist on Linux, so `le open` could never have worked there.)
func browserCommand(url string) (string, []string) { return "xdg-open", []string{url} }

// systemd-resolved's cache flush; the common case on modern distributions.
// Where resolved is absent the command fails with its own message, which
// runSystem surfaces as-is.
var (
	flushDNSExe  = "resolvectl"
	flushDNSArgs = []string{"flush-caches"}
)
