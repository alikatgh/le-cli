package cmd

import (
	"github.com/alikatgh/le-cli/companion"
	"github.com/spf13/cobra"
)

func companionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use: "companion", Short: "Connect the optional Mac app to this foreground CLI session",
		Long: "Start the optional local Mac app companion. Select the generated connection\nfile in Localhost Explorer. Keep this command running; Ctrl-C disconnects.\nNo background agent is installed.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return companion.Run(cmd.Context(), version, cmd.OutOrStdout())
		},
	}
}
