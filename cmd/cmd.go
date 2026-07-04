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
			"smarts to stop each the right way (TERM, brew services, docker, or\n" +
			"launchctl bootout).\n\n" +
			"The rule underneath every stop: identity must be proven, twice. le\n" +
			"re-verifies the PID's start time immediately before signalling, so a\n" +
			"recycled PID never gets a signal meant for something else — and when\n" +
			"identity can't be proven, le refuses rather than guesses. That is\n" +
			"the difference from `lsof | kill`.\n\n" +
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
			if cfg.Theme != "" && !ui.ApplyTheme(cfg.Theme) {
				fmt.Fprintf(cmd.ErrOrStderr(), "le: unknown theme %q in config (themes: %s) — using default\n", cfg.Theme, strings.Join(ui.ThemeNames(), " / "))
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
		Use:     "list [filter]",
		Aliases: []string{"ls"},
		Short:   "Print a one-shot table of listeners (no TUI)",
		Long: "Print every localhost listener as a table (or JSON). An optional\n" +
			"filter narrows it to rows matching that text in the port, name,\n" +
			"command, folder, or owner — the same match the TUI's / filter uses.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rows := gather()
			if len(args) == 1 {
				rows = filterRows(rows, args[0])
			}
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

// filterRows keeps the rows matching q (case-insensitive substring) across the
// port, identity, command, folder, and owner fields — the same set the TUI's
// filter searches, so `le list node` and typing "node" in the TUI agree.
func filterRows(rows []row, q string) []row {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return rows
	}
	// Non-nil even when nothing matches, so `le list <filter> --json` emits []
	// (not null) — matching the unfiltered zero-listener case and keeping jq /
	// JSON.parse consumers happy.
	out := make([]row, 0, len(rows))
	for _, r := range rows {
		hay := strings.ToLower(strings.Join([]string{
			strings.Join(r.Ports, " "), r.Profile.Identity, r.Command, r.CommandLine, r.Cwd, string(r.Profile.Source),
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, r)
		}
	}
	return out
}

func stopCmd() *cobra.Command {
	var dir string
	var dryRun, asJSON bool
	c := &cobra.Command{
		Use:   "stop [port|pid]",
		Short: "Stop a listener (by port/pid) or every listener under a directory",
		Long: "Stop a listener on a port (or the process with a PID), each via its\n" +
			"recommended strategy (TERM / brew services / docker / launchctl). The\n" +
			"PID's start time is re-verified immediately before any signal, so a\n" +
			"recycled PID is never the one that gets signalled — and when identity\n" +
			"can't be proven, le refuses and asks you to rescan instead of guessing.\n\n" +
			"With --dir, stop every listener whose working directory is that path or\n" +
			"nested under it — the terminal equivalent of the app's folder-stop, for\n" +
			"clearing out everything a project spun up.\n\n" +
			"Use --dry-run to see exactly what would be stopped without touching\n" +
			"anything — worth doing before a --dir sweep. --json emits the per-\n" +
			"listener outcomes (or the dry-run preview) as an array for scripts.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir != "" {
				if len(args) > 0 {
					return fmt.Errorf("pass either a port/pid or --dir, not both")
				}
				return runStopDir(dir, dryRun, asJSON)
			}
			if len(args) != 1 {
				return fmt.Errorf("give a port, a pid, or --dir <path>")
			}
			return runStop(args[0], dryRun, asJSON)
		},
	}
	c.Flags().StringVarP(&dir, "dir", "d", "", "stop every listener whose working directory is under this path")
	c.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print what would be stopped without stopping it")
	c.Flags().BoolVar(&asJSON, "json", false, "output per-listener results as JSON")
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
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "wait <port>",
		Short: "Block until a port frees up",
		Long:  "Block until <port> is free. With --timeout, give up after that long and\nexit non-zero — so a script can bound the wait instead of hanging.",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return tools.WaitFree(args[0], timeout) },
	}
	c.Flags().DurationVarP(&timeout, "timeout", "t", 0, "give up after this long (e.g. 30s); 0 waits forever")
	return c
}

func readyCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "ready <port>",
		Short: "Block until something starts listening (open-when-ready)",
		Long:  "Block until something is listening on <port>. With --timeout, give up\nafter that long and exit non-zero.",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return tools.WaitListening(args[0], timeout) },
	}
	c.Flags().DurationVarP(&timeout, "timeout", "t", 0, "give up after this long (e.g. 30s); 0 waits forever")
	return c
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

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
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
	home, _ := os.UserHomeDir()
	_, _ = fmt.Fprintf(w, "%-7s  %-7s  %-22s  %-26s  %-7s  %-8s  %s\n", "PORT", "PID", "WHAT", "DIR", "RISK", "OWNER", "STOP WITH")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%-7s  %-7d  %-22s  %-26s  %-7s  %-8s  %s\n",
			portCell(r.Ports), r.PID, truncate(r.Profile.Identity, 22),
			dirCell(r.Cwd, home, 26), string(r.Profile.Risk),
			string(r.Profile.Source), truncate(r.Profile.StopLabel, 40))
	}
}

// dirCell renders a working directory for the table: home-abbreviated
// ("~/code/app") and truncated from the LEFT, because a path's identity
// lives in its trailing components — "…/big-app/api" beats "/Users/me/co…".
func dirCell(cwd, home string, n int) string {
	if strings.TrimSpace(cwd) == "" {
		return "-"
	}
	if home != "" {
		if cwd == home {
			cwd = "~"
		} else if strings.HasPrefix(cwd, home+string(filepath.Separator)) {
			cwd = "~" + cwd[len(home):]
		}
	}
	r := []rune(cwd)
	if len(r) <= n {
		return cwd
	}
	return "…" + string(r[len(r)-(n-1):])
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

func runStop(target string, dryRun, asJSON bool) error {
	rows := gather()
	matched := matchRows(rows, target)
	if len(matched) == 0 {
		return fmt.Errorf("nothing listening on %s", target)
	}
	return dispatchStop(matched, dryRun, asJSON)
}

// dispatchStop is the shared tail of the port/pid and --dir paths: render the
// dry-run preview or execute the stops, as text or JSON.
func dispatchStop(matched []row, dryRun, asJSON bool) error {
	if dryRun {
		if asJSON {
			return previewMatchedJSON(os.Stdout, matched)
		}
		previewMatched(os.Stdout, matched)
		return nil
	}
	if asJSON {
		return stopMatchedJSON(os.Stdout, matched, kill.Stop)
	}
	return stopMatched(os.Stdout, os.Stderr, matched, kill.Stop)
}

func runStopDir(dir string, dryRun, asJSON bool) error {
	rows := gather()
	matched := matchDir(rows, dir)
	if len(matched) == 0 {
		return fmt.Errorf("no listeners have a working directory under %s", dir)
	}
	return dispatchStop(matched, dryRun, asJSON)
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

// resolvePath canonicalizes a path to an absolute, symlink-resolved form.
// The Abs step is essential: `--dir .` (the most natural invocation) and any
// other relative path must be absolutized before comparison, since the cwds
// it's matched against — from lsof — are always absolute. EvalSymlinks alone
// does NOT absolutize. Falls back to Clean when a step fails (path doesn't
// exist, permission) so matching still works best-effort.
func resolvePath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
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
	// Anchor on a separator so /a/b matches /a/b/c but not /a/bc. Clean strips
	// a trailing separator EXCEPT on root ("/"), so only append one when d
	// doesn't already end in it — otherwise `--dir /` becomes "//" and matches
	// nothing instead of every absolute path.
	sep := string(filepath.Separator)
	if !strings.HasSuffix(d, sep) {
		d += sep
	}
	return strings.HasPrefix(c, d)
}

// stopMatched runs stop over each matched row, printing a ✓/✗ line per
// result, and returns an error naming the partial-failure count if any row
// couldn't be stopped. Split out (with injected writers + stop func) so the
// aggregation and output — including the "N of M could not be stopped"
// partial-failure path — are testable without touching real processes.
// stopResult is one element of `le stop --json` output. lowerCamelCase keys,
// matching scan.Listener / intel.Profile, so scripts see one convention.
type stopResult struct {
	PID      int      `json:"pid"`
	Identity string   `json:"identity"`
	Ports    []string `json:"ports"`
	Action   string   `json:"action"` // what ran (or would run, for a dry run)
	DryRun   bool     `json:"dryRun"`
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"` // set when ok is false
}

// previewMatchedJSON is --dry-run --json: every row is previewable, nothing
// is executed, so ok is always true and action is the recommended strategy.
func previewMatchedJSON(w io.Writer, matched []row) error {
	results := make([]stopResult, 0, len(matched))
	for _, r := range matched {
		results = append(results, stopResult{
			PID: r.PID, Identity: r.Profile.Identity, Ports: r.Ports,
			Action: r.Profile.StopLabel, DryRun: true, OK: true,
		})
	}
	return printJSON(w, results)
}

// stopMatchedJSON executes the stops like stopMatched but reports each
// outcome as JSON on w instead of ✓/✗ lines. The exit-code contract is the
// same: an error naming the partial-failure count when any row failed.
func stopMatchedJSON(w io.Writer, matched []row, stop func(scan.Listener, intel.Profile) (string, error)) error {
	results := make([]stopResult, 0, len(matched))
	var failed int
	for _, r := range matched {
		res := stopResult{PID: r.PID, Identity: r.Profile.Identity, Ports: r.Ports}
		msg, err := stop(r.Listener, r.Profile)
		if err != nil {
			res.Action, res.Error = r.Profile.StopLabel, err.Error()
			failed++
		} else {
			res.Action, res.OK = msg, true
		}
		results = append(results, res)
	}
	if err := printJSON(w, results); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d could not be stopped", failed, len(matched))
	}
	return nil
}

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
