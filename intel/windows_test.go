package intel

import (
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

func TestWindowsArgv0HandlesQuotedProgramFilesPaths(t *testing.T) {
	cases := map[string]string{
		`"C:\Program Files\nodejs\node.exe" server.js --port 3000`: `C:\Program Files\nodejs\node.exe`,
		`C:\Windows\System32\svchost.exe -k netsvcs -p`:            `C:\Windows\System32\svchost.exe`,
		`  "C:\Users\a\AppData\Local\Programs\Foo\foo.exe"`:        `C:\Users\a\AppData\Local\Programs\Foo\foo.exe`,
		// Unquoted with a space in the path: the .exe is the boundary, not the space.
		`C:\Users\a\AppData\Local\Programs\Microsoft VS Code\Code.exe --type=utility`: `C:\Users\a\AppData\Local\Programs\Microsoft VS Code\Code.exe`,
		`C:\Tools\my server.exe arg1 arg2`:                                            `C:\Tools\my server.exe`,
		`Memory Compression`:                                                          `Memory Compression`,
		`System`:                                                                      `System`,
		``:                                                                            ``,
	}
	for in, want := range cases {
		if got := windowsArgv0(in); got != want {
			t.Errorf("windowsArgv0(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsWindowsSystem(t *testing.T) {
	cases := []struct {
		cmd, user string
		want      bool
	}{
		{`c:\windows\system32\svchost.exe -k netsvcs`, `nt authority\system`, true},
		{`c:\windows\system32\lsass.exe`, ``, true},
		{`system`, ``, true},
		{`system idle process`, ``, true},
		// SYSTEM-owned but installed software: a database, not the OS.
		{`"c:\program files\postgresql\16\bin\postgres.exe" -d`, `nt authority\network service`, false},
		{`"c:\program files\mongodb\server\7.0\bin\mongod.exe" --config x`, `nt authority\system`, false},
		// A user's own dev server.
		{`"c:\program files\nodejs\node.exe" server.js`, `desktop\albert`, false},
		{`c:\users\albert\appdata\local\programs\python\python.exe app.py`, `desktop\albert`, false},
		// Unix command lines must never trip the Windows heuristics.
		{`/usr/bin/node app.js`, `root`, false},
		{`/system/library/coreservices/controlcenter.app/contents/macos/controlcenter`, `albert`, false},
	}
	for _, c := range cases {
		if got := isWindowsSystem(c.cmd, c.user); got != c.want {
			t.Errorf("isWindowsSystem(%q, %q) = %v, want %v", c.cmd, c.user, got, c.want)
		}
	}
}

// The unix isSystem entry point must pick these up, so the Windows OS
// processes classify as high-risk / avoid exactly as macOS ones do.
func TestIsSystemSeesWindowsProcesses(t *testing.T) {
	sys := scan.Listener{PID: 4, CommandLine: `System`, User: `NT AUTHORITY\SYSTEM`}
	if !isSystem(sys) {
		t.Error("the Windows System process should be a system process")
	}
	svc := scan.Listener{PID: 900, CommandLine: `C:\Windows\System32\svchost.exe -k RPCSS -p`, User: `NT AUTHORITY\NETWORK SERVICE`}
	if !isSystem(svc) {
		t.Error("svchost should be a system process")
	}
	dev := scan.Listener{PID: 9100, CommandLine: `"C:\Program Files\nodejs\node.exe" server.js`, User: `DESKTOP\albert`}
	if isSystem(dev) {
		t.Error("a user's node server is not a system process")
	}
}

func TestArgv0BaseUnderstandsBothSeparators(t *testing.T) {
	if got := argv0Base(`c:\windows\system32\svchost.exe -k x`); got != "svchost.exe" {
		t.Errorf("backslash path: got %q", got)
	}
	if got := argv0Base(`/usr/libexec/rapportd --flag`); got != "rapportd" {
		t.Errorf("slash path: got %q", got)
	}
}

func TestAppNameOnWindowsInstallRoots(t *testing.T) {
	cases := map[string]string{
		`"C:\Program Files\Docker\Docker\resources\com.docker.backend.exe" -watchdog`: "Docker",
		`"C:\Program Files (x86)\Steam\steam.exe"`:                                    "Steam",
		`C:\Users\a\AppData\Local\Programs\Microsoft VS Code\Code.exe --type=utility`: "Microsoft VS Code",
		`"C:\Users\a\AppData\Local\Discord\app-1.0\Discord.exe" --type=renderer`:      "Discord",
		// The install-root order matters: Programs\ wins over the bare Local\ vendor.
		`C:\Users\a\AppData\Local\Programs\Ollama\ollama.exe serve`: "Ollama",
		// Nothing to name: a bare Windows binary, or a unix line (unchanged behaviour).
		`C:\Windows\System32\svchost.exe -k netsvcs`: "",
		`"C:\Program Files\node.exe"`:                "",
		`/usr/bin/node app.js`:                       "",
	}
	for in, want := range cases {
		if got := AppName(in); got != want {
			t.Errorf("AppName(%q) = %q, want %q", in, got, want)
		}
	}
	// And the macOS path is untouched by the new branch.
	if got := AppName("/Applications/Figma.app/Contents/MacOS/Figma --flag"); got != "Figma" {
		t.Errorf("macOS naming regressed: %q", got)
	}
}
