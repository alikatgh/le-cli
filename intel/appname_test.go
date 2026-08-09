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

// A bundle path in an ARGUMENT must never name the process. The first case
// below is the one that matters and the one the original test missed: when
// argv[0] is NOT a bundle, a later .app path used to win and produce a
// confidently wrong identity — strictly worse than the generic label.
func TestAppNameOnlyReadsArgvZero(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"unbundled executable with a bundle argument",
			"/usr/bin/tool --open /Applications/Other.app/Contents/MacOS/Other",
			"",
		},
		{
			"open(1) launching an app is still open(1)",
			"/usr/bin/open -a /Applications/Slack.app",
			"",
		},
		{
			"bundle in argv[0] wins over one in the arguments",
			"/Applications/Antigravity IDE.app/Contents/MacOS/Electron --inspect /Applications/Other.app/x",
			"Antigravity IDE",
		},
		{
			"vendor directory in an argument is ignored too",
			"/usr/local/bin/rsync /Users/me/Library/Application Support/Figma/",
			"",
		},
		{
			"relative command with a bundle argument",
			"node --require /Applications/Other.app/x.js",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AppName(c.in); got != c.want {
				t.Errorf("AppName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The prefix must be where the executable LIVES, not merely present in the
// string — otherwise a path that mentions /Applications/ deeper down slips in.
func TestAppNameRequiresTheBundleAtArgvZeroRoot(t *testing.T) {
	if got := AppName("/opt/wrapper/Applications/Fake.app/Contents/MacOS/Fake"); got != "Fake" {
		// This one legitimately resolves via the generic bundle fallback —
		// it IS argv[0] and it IS a bundle, just not under /Applications.
		t.Errorf("AppName = %q, want Fake from the generic bundle rule", got)
	}
}
