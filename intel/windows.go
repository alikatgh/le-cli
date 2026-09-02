package intel

import "strings"

// Windows-shaped classification helpers. Untagged: pure string handling, so
// it runs — and is tested — on the macOS machines this is developed on. The
// unix heuristics in isSystem read /System/ and root; these are their Windows
// counterparts and are simply false for a unix command line.

// windowsArgv0 returns the executable path from a Windows command line.
//
// Win32_Process.CommandLine preserves quoting, and a path under Program Files
// contains a space, so a quoted argv[0] ends at its closing quote. Unquoted
// ones do occur (some launchers never quote), and there the space cannot be
// the boundary; instead argv[0] ends at the first ".exe" followed by a space
// or the end of the line. A name with no ".exe" at all — System, "System Idle
// Process", "Memory Compression" — is a kernel pseudo-process with no
// arguments, so it ends at the first flag boundary (" -" or " /"), and
// otherwise IS the whole line.
func windowsArgv0(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.Index(cmd[1:], `"`); end >= 0 {
			return cmd[1 : end+1]
		}
		return strings.TrimPrefix(cmd, `"`)
	}
	lower := strings.ToLower(cmd)
	for from := 0; ; {
		i := strings.Index(lower[from:], ".exe")
		if i < 0 {
			break
		}
		end := from + i + len(".exe")
		if end == len(cmd) || cmd[end] == ' ' {
			return cmd[:end]
		}
		from = end
	}
	end := len(cmd)
	for _, sep := range []string{" -", " /"} {
		if i := strings.Index(cmd, sep); i >= 0 && i < end {
			end = i
		}
	}
	return cmd[:end]
}

// baseAny is a basename that understands both separators.
func baseAny(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// isWindowsSystem is the Windows reading of "this is the OS, not you":
// anything running out of \Windows\, the handful of kernel-adjacent names
// that own ports on every machine, or a process the OS runs as one of its
// own service accounts — EXCEPT when that process lives under Program Files
// or a user profile, where a SYSTEM-owned Postgres or MongoDB service is a
// database, not the operating system (the same carve-out the root check makes
// for /Cellar/ and /Users/). Expects lowercased inputs.
func isWindowsSystem(cmdLower, userLower string) bool {
	exe := windowsArgv0(cmdLower)
	if strings.Contains(exe, `:\windows\`) {
		return true
	}
	switch baseAny(exe) {
	case "system", "system idle process", "svchost.exe", "wininit.exe", "services.exe",
		"lsass.exe", "csrss.exe", "smss.exe", "winlogon.exe", "spoolsv.exe", "dns.exe":
		return true
	}
	switch userLower {
	case `nt authority\system`, `nt authority\local service`, `nt authority\network service`:
		return !strings.Contains(exe, `\program files`) && !strings.Contains(exe, `\users\`)
	}
	return false
}
