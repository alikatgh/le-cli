// Package cmd wires le's Cobra command tree: the root opens the TUI, and the
// subcommands (list, stop, hold, wait, ready) cover the scriptable surface.
// Cobra supplies per-command --help and shell completions for free.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/alikatgh/le-cli/config"
	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/kill"
	"github.com/alikatgh/le-cli/scan"
	"github.com/alikatgh/le-cli/tools"
	"github.com/alikatgh/le-cli/ui"
)

// How long a config-warning stays on screen before the TUI takes over the
// terminal via its alt-screen switch — long enough to read one line, short
// enough not to feel like a hang.
const configWarningPause = 1500 * time.Millisecond

// Execute runs the root command. version is injected by main.
func Execute(version string) {
	if err := newRoot(version).Execute(); err != nil {
		os.Exit(1)
	}
}

func newRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "le",
		Short: "See and stop what's listening on localhost",
		Long: "le — a fast, keyboard-driven view of localhost listeners, with the\n" +
			"smarts to stop each the right way (TERM, brew services, or docker).\n\n" +
			"Run with no arguments to open the live TUI.",
		Version:      version,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, warning := config.Load()
			if warning != "" {
				// Printed here, before ui.Run enters the alt-screen —
				// otherwise this line is swallowed the instant the TUI
				// takes over and a typo'd config becomes invisible.
				fmt.Fprintln(os.Stderr, warning)
				time.Sleep(configWarningPause)
			}
			return ui.Run(ui.Options{Interval: cfg.Interval(), Filter: cfg.Filter})
		},
	}
	root.AddCommand(listCmd(), stopCmd(), holdCmd(), waitCmd(), readyCmd(), versionCmd(version))
	return root
}

func listCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Print a one-shot table of listeners (no TUI)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rows := gather()
			if asJSON {
				return printJSON(rows)
			}
			printTable(rows)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return c
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <port|pid>",
		Short: "Stop a listener the smart way (TERM / brew / docker)",
		Long:  "Stop every listener on a port (or the process with a PID), each via its\nrecommended strategy. The PID is re-checked first, so a recycled PID is\nnever the one that gets signalled.",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runStop(args[0]) },
	}
}

func holdCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hold <port>",
		Short: "Hold a port so nothing else can grab it (Ctrl-C frees it)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return tools.Hold(args[0]) },
	}
}

func waitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait <port>",
		Short: "Block until a port frees up",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return tools.WaitFree(args[0]) },
	}
}

func readyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ready <port>",
		Short: "Block until something starts listening (open-when-ready)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return tools.WaitListening(args[0]) },
	}
}

func versionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the le version",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println("le", version) },
	}
}

// --- shared helpers ---

type row struct {
	scan.Listener
	Profile intel.Profile `json:"profile"`
}

func gather() []row {
	listeners, err := scan.Scan()
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan error:", err)
	}
	env := intel.Detect()
	rows := make([]row, len(listeners))
	for i, l := range listeners {
		rows[i] = row{Listener: l, Profile: intel.Make(l, env)}
	}
	return rows
}

func printJSON(rows []row) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func printTable(rows []row) {
	if len(rows) == 0 {
		fmt.Println("No localhost listeners found.")
		return
	}
	fmt.Printf("%-7s  %-7s  %-22s  %-7s  %-8s  %s\n", "PORT", "PID", "WHAT", "RISK", "OWNER", "STOP WITH")
	for _, r := range rows {
		fmt.Printf("%-7s  %-7d  %-22s  %-7s  %-8s  %s\n",
			portCell(r.Ports), r.PID, truncate(r.Profile.Identity, 22), string(r.Profile.Risk),
			string(r.Profile.Source), truncate(r.Profile.StopLabel, 40))
	}
}

func portCell(ports []string) string {
	switch len(ports) {
	case 0:
		return "-"
	case 1:
		return ports[0]
	default:
		return ports[0] + " +" + strconv.Itoa(len(ports)-1)
	}
}

func runStop(target string) error {
	rows := gather()
	var matched []row
	for _, r := range rows { // port match first
		for _, p := range r.Ports {
			if p == target {
				matched = append(matched, r)
				break
			}
		}
	}
	if len(matched) == 0 { // fall back to PID
		if pid, err := strconv.Atoi(target); err == nil {
			for _, r := range rows {
				if r.PID == pid {
					matched = append(matched, r)
				}
			}
		}
	}
	if len(matched) == 0 {
		return fmt.Errorf("nothing listening on %s", target)
	}
	var failed int
	for _, r := range matched {
		msg, err := kill.Stop(r.Listener, r.Profile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s (pid %d): %v\n", r.Profile.Identity, r.PID, err)
			failed++
			continue
		}
		fmt.Printf("✓ %s (pid %d) — %s\n", r.Profile.Identity, r.PID, msg)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d could not be stopped", failed, len(matched))
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n < 1 {
		return ""
	}
	// Rune-based, not byte-based: a byte-length slice can cut a multi-byte
	// UTF-8 character in half (accented paths, CJK project dirs, emoji-
	// branded container names), producing invalid UTF-8 in the output.
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
