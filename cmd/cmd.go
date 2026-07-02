// Package cmd wires le's Cobra command tree: the root opens the TUI, and the
// subcommands (list, stop, hold, wait, ready) cover the scriptable surface.
// Cobra supplies per-command --help and shell completions for free.
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// NewRootForDocs builds the same command tree Execute uses, for tools that
// need it without actually running the CLI (e.g. the man page generator in
// internal/gendocs).
func NewRootForDocs() *cobra.Command {
	return newRoot("dev")
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
				return printJSON(os.Stdout, rows)
			}
			printTable(os.Stdout, rows)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return c
}

func stopCmd() *cobra.Command {
	var dir string
	var dryRun bool
	c := &cobra.Command{
		Use:   "stop [port|pid]",
		Short: "Stop a listener (by port/pid) or every listener under a directory",
		Long: "Stop a listener on a port (or the process with a PID), each via its\n" +
			"recommended strategy (TERM / brew services / docker). The PID is\n" +
			"re-checked first, so a recycled PID is never the one that gets signalled.\n\n" +
			"With --dir, stop every listener whose working directory is that path or\n" +
			"nested under it — the terminal equivalent of the app's folder-stop, for\n" +
			"clearing out everything a project spun up.\n\n" +
			"Use --dry-run to see exactly what would be stopped without touching\n" +
			"anything — worth doing before a --dir sweep.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir != "" {
				if len(args) > 0 {
					return fmt.Errorf("pass either a port/pid or --dir, not both")
				}
				return runStopDir(dir, dryRun)
			}
			if len(args) != 1 {
				return fmt.Errorf("give a port, a pid, or --dir <path>")
			}
			return runStop(args[0], dryRun)
		},
	}
	c.Flags().StringVarP(&dir, "dir", "d", "", "stop every listener whose working directory is under this path")
	c.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print what would be stopped without stopping it")
	return c
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

func printJSON(w io.Writer, rows []row) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

func printTable(w io.Writer, rows []row) {
	// Errors from writing to w are deliberately discarded, same as
	// tools.Hold's listener Close below — a broken pipe (e.g. `le list |
	// head`) means every subsequent write fails identically, and this
	// function has nothing more useful to do about it than to keep going.
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(w, "No localhost listeners found.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-7s  %-7s  %-22s  %-7s  %-8s  %s\n", "PORT", "PID", "WHAT", "RISK", "OWNER", "STOP WITH")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%-7s  %-7d  %-22s  %-7s  %-8s  %s\n",
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

// matchRows finds every row addressed by target: a port match if any row
// listens on it, otherwise (never both) a fallback to PID. Port takes
// priority so a target that happens to look like both a live port and
// someone else's PID resolves the way a human would expect it to.
func matchRows(rows []row, target string) []row {
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
	return matched
}

func runStop(target string, dryRun bool) error {
	rows := gather()
	matched := matchRows(rows, target)
	if len(matched) == 0 {
		return fmt.Errorf("nothing listening on %s", target)
	}
	if dryRun {
		previewMatched(os.Stdout, matched)
		return nil
	}
	return stopMatched(os.Stdout, os.Stderr, matched, kill.Stop)
}

func runStopDir(dir string, dryRun bool) error {
	rows := gather()
	matched := matchDir(rows, dir)
	if len(matched) == 0 {
		return fmt.Errorf("no listeners have a working directory under %s", dir)
	}
	if dryRun {
		previewMatched(os.Stdout, matched)
		return nil
	}
	return stopMatched(os.Stdout, os.Stderr, matched, kill.Stop)
}

// previewMatched lists what a stop WOULD act on, without touching anything —
// the --dry-run body, split out (with an injected writer) so it's testable
// and so both the port/pid and --dir paths render it identically.
func previewMatched(w io.Writer, matched []row) {
	for _, r := range matched {
		_, _ = fmt.Fprintf(w, "would stop %s (pid %d) — %s\n", r.Profile.Identity, r.PID, r.Profile.StopLabel)
	}
}

// matchDir returns every row whose working directory is dir or nested under
// it. Both sides are symlink-resolved first, so a listener started in a
// symlinked project path still matches the real path the user passes (and
// vice-versa) — the folder-match bug the macOS app already learned to handle.
func matchDir(rows []row, dir string) []row {
	target := resolvePath(dir)
	var out []row
	for _, r := range rows {
		if withinDir(resolvePath(r.Cwd), target) {
			out = append(out, r)
		}
	}
	return out
}

// resolvePath canonicalizes a path via EvalSymlinks, falling back to a plain
// Clean when the path can't be resolved (doesn't exist, permission, etc.) so
// matching still works on a best-effort basis.
func resolvePath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// withinDir reports whether cwd is dir or a descendant of it, comparing
// cleaned, separator-anchored paths — so /a/b matches /a/b/c but NOT /a/bc.
func withinDir(cwd, dir string) bool {
	if cwd == "" || dir == "" {
		return false
	}
	c := filepath.Clean(cwd)
	d := filepath.Clean(dir)
	if c == d {
		return true
	}
	return strings.HasPrefix(c, d+string(filepath.Separator))
}

// stopMatched runs stop over each matched row, printing a ✓/✗ line per
// result, and returns an error naming the partial-failure count if any row
// couldn't be stopped. Split out (with injected writers + stop func) so the
// aggregation and output — including the "N of M could not be stopped"
// partial-failure path — are testable without touching real processes.
func stopMatched(w, errW io.Writer, matched []row, stop func(scan.Listener, intel.Profile) (string, error)) error {
	// Write errors are discarded, same as printTable: a broken output pipe
	// fails every subsequent write identically and there's nothing more
	// useful to do here than finish the stop attempts.
	var failed int
	for _, r := range matched {
		msg, err := stop(r.Listener, r.Profile)
		if err != nil {
			_, _ = fmt.Fprintf(errW, "✗ %s (pid %d): %v\n", r.Profile.Identity, r.PID, err)
			failed++
			continue
		}
		_, _ = fmt.Fprintf(w, "✓ %s (pid %d) — %s\n", r.Profile.Identity, r.PID, msg)
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
