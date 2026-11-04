package ui

import (
	"bytes"
	"context"
	"image/color"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

type terminalColorProbe struct{}

func (m terminalColorProbe) Init() tea.Cmd                       { return tea.Quit }
func (m terminalColorProbe) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m terminalColorProbe) View() tea.View {
	return tea.NewView(lipgloss.NewStyle().Foreground(lipgloss.Color("#E0218A")).Render("color probe"))
}

// Exercise the actual Bubble Tea output layer: Lip Gloss v2 always renders
// full color strings, and the explicit profile must remove colors at output.
// A TrueColor baseline proves non-opt-out cases retain the existing profile.
func TestTerminalColorOptOutPreservesV1EnvironmentConventions(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		noColor bool
	}{
		{"default", nil, false},
		{"empty NO_COLOR", map[string]string{"NO_COLOR": ""}, false},
		{"NO_COLOR Y", map[string]string{"NO_COLOR": "Y"}, true},
		{"NO_COLOR zero", map[string]string{"NO_COLOR": "0"}, true},
		{"NO_COLOR arbitrary", map[string]string{"NO_COLOR": "anything"}, true},
		{"NO_COLOR false", map[string]string{"NO_COLOR": "false"}, true},
		{"NO_COLOR wins over force", map[string]string{"NO_COLOR": "Y", "CLICOLOR_FORCE": "1"}, true},
		{"CLICOLOR zero", map[string]string{"CLICOLOR": "0"}, true},
		{"CLICOLOR zero force zero", map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "0"}, true},
		{"CLICOLOR one", map[string]string{"CLICOLOR": "1"}, false},
		{"force wins over CLICOLOR zero", map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "1"}, false},
		{"arbitrary force wins over CLICOLOR zero", map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "Y"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			environ := []string{"TERM=xterm-256color"}
			for name, value := range tc.env {
				environ = append(environ, name+"="+value)
			}
			sort.Strings(environ)
			trueColor := colorprofile.TrueColor
			got := terminalColorProbeOutput(t, environ, &trueColor)
			hasColor := terminalHasForegroundColor(got)
			if hasColor == tc.noColor {
				t.Fatalf("terminal color output present=%v, want %v", hasColor, !tc.noColor)
			}
		})
	}
}

// These probes use actual capability detection rather than a forced profile,
// so a boolean-parsing mismatch cannot be hidden by the test harness.
func TestTerminalForcedColorUsesActualDetection(t *testing.T) {
	cases := []struct {
		name          string
		environ       []string
		wantColor     bool
		wantTrueColor bool
	}{
		{"dumb", []string{"TERM=dumb"}, false, false},
		{"dumb force Y", []string{"TERM=dumb", "CLICOLOR_FORCE=Y"}, true, false},
		{"dumb arbitrary force", []string{"TERM=dumb", "CLICOLOR_FORCE=anything"}, true, false},
		{"dumb force zero", []string{"TERM=dumb", "CLICOLOR_FORCE=0"}, false, false},
		{"force overrides CLICOLOR zero", []string{"TERM=dumb", "CLICOLOR=0", "CLICOLOR_FORCE=Y"}, true, false},
		{"NO_COLOR wins", []string{"TERM=dumb", "NO_COLOR=0", "CLICOLOR_FORCE=Y"}, false, false},
		{"force preserves truecolor", []string{"TERM=xterm-256color", "COLORTERM=truecolor", "CLICOLOR_FORCE=Y"}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string(nil), tc.environ...)
			got := terminalColorProbeOutput(t, tc.environ, nil)
			if terminalHasForegroundColor(got) != tc.wantColor {
				t.Fatalf("color output present=%v, want %v", terminalHasForegroundColor(got), tc.wantColor)
			}
			if tc.wantTrueColor && !strings.Contains(got, "38;2;") && !strings.Contains(got, "38:2:") {
				t.Fatal("forced color downgraded detected truecolor capabilities")
			}
			if !reflect.DeepEqual(tc.environ, original) {
				t.Fatal("terminal color normalization mutated the caller's environment")
			}
		})
	}
}

func terminalColorProbeOutput(t *testing.T, environ []string, profile *colorprofile.Profile) string {
	t.Helper()
	return terminalColorProbeOutputForPlatform(t, environ, profile, runtime.GOOS == "windows")
}

func terminalColorProbeOutputForPlatform(t *testing.T, environ []string, profile *colorprofile.Profile, windows bool) string {
	t.Helper()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	options := []tea.ProgramOption{
		tea.WithInput(nil), tea.WithOutput(&out), tea.WithWindowSize(40, 4),
		tea.WithoutSignalHandler(), tea.WithContext(ctx),
	}
	if profile != nil {
		options = append(options, tea.WithColorProfile(*profile))
	}
	options = append(options, terminalColorOptionsForPlatform(environ, windows)...)
	if _, err := tea.NewProgram(terminalColorProbe{}, options...).Run(); err != nil {
		t.Fatalf("terminal probe failed: %v", err)
	}
	got := out.String()
	if !strings.Contains(ansi.Strip(got), "color probe") {
		t.Fatal("terminal probe did not render its content")
	}
	return got
}

// Parse SGR parameters so all foreground encodings count, without treating
// RGB components in a background/underline color as foreground attributes.
func terminalHasForegroundColor(output string) bool {
	found := false
	p := ansi.NewParser()
	p.SetHandler(ansi.Handler{HandleCsi: func(cmd ansi.Cmd, params ansi.Params) {
		if cmd.Final() != 'm' {
			return
		}
		for i := 0; i < len(params); i++ {
			n := params[i].Param(0)
			if (n >= 30 && n <= 37) || (n >= 90 && n <= 97) || n == 38 {
				found = true
				return
			}
			if n == 48 || n == 58 {
				var ignored color.Color
				if consumed := ansi.ReadStyleColor(params[i:], &ignored); consumed > 0 {
					i += consumed - 1
				}
			}
		}
	}})
	p.Parse([]byte(output))
	return found
}

func TestTerminalForegroundColorDetection(t *testing.T) {
	for _, sequence := range []string{
		"\x1b[31m", "\x1b[97m", "\x1b[1;32m", "\x1b[38;5;200m",
		"\x1b[38;2;224;33;138m", "\x1b[38:5:200m", "\x1b[38:2::224:33:138m",
	} {
		if !terminalHasForegroundColor(sequence) {
			t.Errorf("foreground color not recognized: %q", sequence)
		}
	}
	for _, sequence := range []string{
		"plain text", "\x1b[1m", "\x1b[39m", "\x1b[?25l",
		"\x1b[48;2;31;32;33m", "\x1b[48:2::31:32:33m", "\x1b[58;5;31m",
	} {
		if terminalHasForegroundColor(sequence) {
			t.Errorf("non-foreground sequence recognized as color: %q", sequence)
		}
	}
}

func TestTerminalColorEnvironmentNamesFollowPlatform(t *testing.T) {
	cases := []struct {
		name      string
		windows   bool
		environ   []string
		wantColor bool
	}{
		{"Windows lowercase NO_COLOR", true, []string{"TERM=dumb", "no_color=Y", "CLICOLOR_FORCE=1"}, false},
		{"Unix lowercase NO_COLOR ignored", false, []string{"TERM=dumb", "no_color=Y", "CLICOLOR_FORCE=1"}, true},
		{"Windows mixed NO_COLOR dominates force", true, []string{"TERM=dumb", "No_CoLoR=0", "cLiCoLoR_fOrCe=Y"}, false},
		{"Unix mixed NO_COLOR ignored", false, []string{"TERM=dumb", "No_CoLoR=0", "CLICOLOR_FORCE=1"}, true},
		{"Windows lowercase CLICOLOR", true, []string{"TERM=xterm-256color", "TTY_FORCE=1", "clicolor=0"}, false},
		{"Unix lowercase CLICOLOR ignored", false, []string{"TERM=xterm-256color", "TTY_FORCE=1", "clicolor=0"}, true},
		{"Windows lowercase force", true, []string{"TERM=dumb", "clicolor_force=Y"}, true},
		{"Unix lowercase force ignored", false, []string{"TERM=dumb", "clicolor_force=Y"}, false},
		{"Windows mixed force", true, []string{"TERM=dumb", "cLiCoLoR_fOrCe=anything"}, true},
		{"Unix mixed force ignored", false, []string{"TERM=dumb", "cLiCoLoR_fOrCe=anything"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string(nil), tc.environ...)
			got := terminalColorProbeOutputForPlatform(t, tc.environ, nil, tc.windows)
			if terminalHasForegroundColor(got) != tc.wantColor {
				t.Fatalf("color output present=%v, want %v", terminalHasForegroundColor(got), tc.wantColor)
			}
			if !reflect.DeepEqual(tc.environ, original) {
				t.Fatal("color normalization mutated the original environment")
			}
		})
	}
}

func TestTerminalColorNormalizationPreservesUnrelatedEnvironment(t *testing.T) {
	original := []string{"no_color=Y", "CliColor=0", "clicolor_force=anything", "Other_Name=MiXeD=value", "entry-without-equals"}
	before := append([]string(nil), original...)
	want := []string{"NO_COLOR=Y", "CLICOLOR=0", "CLICOLOR_FORCE=1", "Other_Name=MiXeD=value", "entry-without-equals"}
	if got := normalizeTerminalColorEnv(original, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows normalization = %q, want %q", got, want)
	}
	if got := normalizeTerminalColorEnv(original, false); !reflect.DeepEqual(got, before) {
		t.Fatalf("Unix normalization changed case-sensitive environment: %q", got)
	}
	if !reflect.DeepEqual(original, before) {
		t.Fatal("normalization mutated the original environment")
	}
}
