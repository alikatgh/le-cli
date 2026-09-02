//go:build darwin

package tools

// browserCommand is the app's own launcher: `open <url>`.
func browserCommand(url string) (string, []string) { return "/usr/bin/open", []string{url} }

// The app's Flush DNS tool, verbatim.
var (
	flushDNSExe  = "/usr/bin/dscacheutil"
	flushDNSArgs = []string{"-flushcache"}
)
