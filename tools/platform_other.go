//go:build !darwin && !windows

package tools

// Absolute paths, like the macOS ones: system_test.go pins that every
// housekeeping exe is absolute, so a binary of the same name earlier on a
// user's PATH can never be what runs. Both locations are where xdg-utils and
// systemd install on Debian/Ubuntu, Fedora and Arch.

// xdg-open is the freedesktop launcher every mainstream Linux desktop honours.
// (Before the platform split this path ran /usr/bin/open, which does not
// exist on Linux, so `le open` could never have worked there. LE-CLI-019)
func browserCommand(url string) (string, []string) { return "/usr/bin/xdg-open", []string{url} }

// systemd-resolved's cache flush; the common case on modern distributions.
// Where resolved is absent the command fails with its own message, which
// runSystem surfaces as-is.
var (
	flushDNSExe  = "/usr/bin/resolvectl"
	flushDNSArgs = []string{"flush-caches"}
)
