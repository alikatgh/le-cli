package ui

import (
	"reflect"
	"testing"
)

// Inspect the command without executing it, so hostile-looking paths cannot
// become shell commands and this regression also runs on macOS/Linux CI.
func TestWindowsTerminalCommandKeepsDirectoryOutOfShellText(t *testing.T) {
	for name, directory := range map[string]string{
		"plain":                    `C:\Projects\demo`,
		"spaces":                   `C:\Projects\my project`,
		"ampersand without spaces": `C:\Projects\demo&calc`,
		"ampersand with spaces":    `C:\Projects\demo & calc`,
		"pipe":                     `C:\Projects\demo|calc`,
		"percent":                  `C:\Projects\%TEMP%`,
		"parentheses":              `C:\Projects\(draft)`,
		"combined punctuation":     `C:\Projects\demo&pipe|percent%(draft)`,
	} {
		t.Run(name, func(t *testing.T) {
			command := windowsTerminalCommand(directory)
			if command.Dir != directory {
				t.Fatalf("working directory = %q, want literal %q", command.Dir, directory)
			}
			want := []string{"cmd", "/c", "start", "", "cmd"}
			if !reflect.DeepEqual(command.Args, want) {
				t.Fatalf("shell arguments = %q, want only fixed tokens %q", command.Args, want)
			}
			for _, argument := range command.Args {
				if argument == directory {
					t.Fatal("directory was interpolated into cmd.exe command text")
				}
			}
		})
	}
}
