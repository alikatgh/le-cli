package intel

import "testing"

// Every case here is a REAL command line off a live machine — the eight rows
// that all rendered as "App helper" plus the three that read "Editor language
// service". Fixtures invented from imagination would have missed the nested
// bundle (OneDrive) and the vendor directory (Figma, Adobe, Pulse Secure).
func TestAppName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"top-level bundle", "/Applications/BlueStacks.app/Contents/MacOS/BlueStacks", "BlueStacks"},
		{"non-ascii bundle", "/Applications/企业微信.app/Contents/MacOS/企业微信", "企业微信"},
		{"bundle name with spaces", "/Applications/Grammarly Desktop.app/Contents/Frameworks/ProjectLlamaCore", "Grammarly Desktop"},
		{"nested bundle takes the outer product", "/Applications/OneDrive.app/Contents/OneDrive Sync Service.app/Contents/MacOS/OneDrive Sync Service", "OneDrive"},
		{"editor bundle", "/Applications/Antigravity IDE.app/Contents/Resources/app/extensions/x/language_server_macos_arm", "Antigravity IDE"},
		{"system bundle", "/System/Library/CoreServices/ControlCenter.app/Contents/MacOS/ControlCenter", "ControlCenter"},

		// Vendor directory beats the helper bundle inside it: the product is
		// Figma, not FigmaAgent.
		{"vendor dir wins over nested helper bundle", "/Users/me/Library/Application Support/Figma/FigmaAgent.app/Contents/MacOS/figma_agent", "Figma"},
		{"vendor dir with no bundle at all", "/Library/Application Support/Adobe/Adobe Desktop Common/ADS/Adobe Desktop Service", "Adobe"},
		{"vendor dir with a space", "/Users/me/Library/Application Support/Pulse Secure/SetupClient/PulseSetupClient", "Pulse Secure"},

		// Nothing to name — the caller keeps its generic label.
		{"bare system binary", "/usr/libexec/rapportd", ""},
		{"plain command", "node app.js", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AppName(c.in); got != c.want {
				t.Errorf("AppName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// A bundle path appearing in a LATER argument must not rename the process —
// argv[0] is the process, everything after it is data.
func TestAppNamePrefersArgvZero(t *testing.T) {
	got := AppName("/Applications/Antigravity IDE.app/Contents/MacOS/Electron --inspect /Applications/Other.app/x")
	if got != "Antigravity IDE" {
		t.Errorf("AppName = %q, want the argv[0] bundle", got)
	}
}
