//go:build windows

package tools

import (
	"os"
	"path/filepath"
)

// system32 resolves a Windows system executable to its absolute path under
// %SystemRoot%, for the same reason the unix files use /usr/bin/…: the
// housekeeping exes must not be whatever a same-named binary earlier on PATH
// happens to be. SystemRoot is always set on a real Windows session; the bare
// name is only a fallback for an environment stripped of it.
func system32(exe string) string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return filepath.Join(root, "System32", exe)
	}
	return exe
}

// rundll32 url.dll,FileProtocolHandler hands the URL to the default browser
// WITHOUT a shell in between. The tempting `cmd /c start <url>` is avoided on
// purpose: cmd parses the URL, so an `&` in a query string would be read as a
// command separator. This form takes the URL as one discrete argument.
func browserCommand(url string) (string, []string) {
	return system32("rundll32.exe"), []string{"url.dll,FileProtocolHandler", url}
}

var (
	flushDNSExe  = system32("ipconfig.exe")
	flushDNSArgs = []string{"/flushdns"}
)
