package cmd

import (
	"io"

	"github.com/alikatgh/le-cli/tools"
	"github.com/spf13/cobra"
)

func textCmd() *cobra.Command {
	return textCmdWithTransformer(tools.TextTransformer{})
}

func textCmdWithTransformer(transformer tools.TextTransformer) *cobra.Command {
	parent := &cobra.Command{
		Use:   "text",
		Short: "Transform UTF-8 stdin or generate portable text",
		Long: "Transform UTF-8 text from stdin and write the result to stdout.\n" +
			"Each input and output is limited to 1 MiB; JSON nesting is limited to 128.\n" +
			"Invalid input produces no partial result. These tools never use the clipboard.\n" +
			"Scalar values and reports end in LF; content transforms preserve their content.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	for _, spec := range []struct {
		name  string
		short string
		input bool
	}{
		{"uuid", "Generate a cryptographically random UUID v4", false},
		{"json", "Pretty-print JSON from stdin with four-space indentation", true},
		{"epoch", "Print the current whole Unix seconds", false},
		{"plain", "Pass UTF-8 stdin through unchanged", true},
		{"base64-encode", "Encode stdin as Base64 with a terminating LF", true},
		{"base64-decode", "Decode canonical Base64 stdin to UTF-8 (ASCII whitespace allowed)", true},
		{"url-encode", "Percent-encode trimmed stdin, preserving unreserved bytes and slash", true},
		{"url-decode", "Decode percent-encoded trimmed stdin; a literal + remains +", true},
		{"count", "Count LF terminators, whitespace-separated words, and Unicode scalars", true},
		{"sha256", "Print the SHA-256 digest of exact UTF-8 stdin", true},
		{"lorem", "Print the native tool's two sample paragraphs", false},
		{"gitignore", "Print a starter .gitignore for macOS, Node, and Python", false},
		{"password", "Print a secret generated from 24 cryptographically random bytes", false},
	} {
		child := &cobra.Command{
			Use:   spec.name,
			Short: spec.short,
			Args:  usageArgs(cobra.NoArgs),
			RunE: func(cmd *cobra.Command, args []string) error {
				var input []byte
				var err error
				if spec.input {
					input, err = tools.ReadTextInput(cmd.InOrStdin())
					if err != nil {
						return err
					}
				}
				return writeTextTransform(cmd, transformer, spec.name, input)
			},
		}
		parent.AddCommand(child)
	}
	var seconds string
	epochToDate := &cobra.Command{
		Use:   "epoch-to-date",
		Short: "Convert whole Unix seconds to ISO-8601 UTC",
		Long: "Convert whole Unix seconds from stdin, or use --seconds.\n" +
			"Zero and negative seconds are supported; valid dates span years 0001 through 9999.\n\n" +
			"  printf '%s' -1 | le text epoch-to-date\n" +
			"  le text epoch-to-date --seconds=-1",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := []byte(seconds)
			if !cmd.Flags().Changed("seconds") {
				var err error
				input, err = tools.ReadTextInput(cmd.InOrStdin())
				if err != nil {
					return err
				}
			}
			return writeTextTransform(cmd, transformer, "epoch-to-date", input)
		},
	}
	epochToDate.Flags().StringVar(&seconds, "seconds", "", "Whole Unix seconds (otherwise read stdin)")
	parent.AddCommand(epochToDate)
	return parent
}

func writeTextTransform(cmd *cobra.Command, transformer tools.TextTransformer, operation string, input []byte) error {
	output, err := transformer.Transform(operation, input)
	if err != nil {
		return err
	}
	written, err := cmd.OutOrStdout().Write(output)
	if err == nil && written != len(output) {
		return io.ErrShortWrite
	}
	return err
}
