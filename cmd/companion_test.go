package cmd

import (
	"runtime"
	"testing"

	"github.com/spf13/cobra"
)

func TestCompanionRegistrationMatchesPlatformAndDocs(t *testing.T) {
	hasCompanion := func(root *cobra.Command) bool {
		for _, child := range root.Commands() {
			if child.Name() == "companion" {
				return true
			}
		}
		return false
	}
	if got := hasCompanion(newRoot("dev", false)); got != (runtime.GOOS == "darwin") {
		t.Fatalf("runtime companion registered=%v on %s", got, runtime.GOOS)
	}
	if !hasCompanion(NewRootForDocs()) {
		t.Fatal("docs must include companion regardless of generation platform")
	}
}
