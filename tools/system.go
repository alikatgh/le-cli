package tools

import (
	"fmt"
	"os/exec"
	"strings"
)

// System utilities that mirror the macOS app's one-shot action tools (Flush
// DNS, Restart Dock/Finder, Sleep display). Each runs the SAME command the app
// runs so `le` is 1:1 with the GUI.

// runSystem runs a one-shot system command and reports a clear result. On
// failure it surfaces the command's own output (or the exec error) rather than
// a bare "exit status 1".
func runSystem(desc, exe string, args ...string) error {
	out, err := exec.Command(exe, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s failed: %s", desc, msg)
	}
	fmt.Println(desc + " done.")
	return nil
}

// FlushDNS clears the macOS DNS cache — /usr/bin/dscacheutil -flushcache,
// matching the app's Flush DNS tool.
func FlushDNS() error { return runSystem("flush DNS", "/usr/bin/dscacheutil", "-flushcache") }

// RestartDock relaunches the Dock — /usr/bin/killall Dock.
func RestartDock() error { return runSystem("restart Dock", "/usr/bin/killall", "Dock") }

// RestartFinder relaunches Finder — /usr/bin/killall Finder.
func RestartFinder() error { return runSystem("restart Finder", "/usr/bin/killall", "Finder") }

// SleepDisplay puts the display to sleep now — /usr/bin/pmset displaysleepnow.
func SleepDisplay() error { return runSystem("sleep display", "/usr/bin/pmset", "displaysleepnow") }
