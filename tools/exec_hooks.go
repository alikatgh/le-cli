package tools

import "os/exec"

// Hooks for the commands in this package that reach outside the process and
// change something a human can see: `killall Dock`, `killall Finder`, `pmset
// displaysleepnow`, `dscacheutil -flushcache`, and `open`, which puts a window
// on the screen.
//
// They are package vars for exactly one reason: so the test binary can neuter
// them (see exec_hooks_guard_test.go). No test calls these functions today —
// but "no test calls it today" is the state the ui package was in before a
// randomised test started pressing the keys bound to `open`, which opened ~876
// Terminal windows on a live desktop in one afternoon (docs/BUG_JOURNAL.md,
// LE-CLI-016). Here the same accident would run `killall Finder`.
//
// The guard belongs at the boundary, not at the call site, because a call-site
// stub only protects the tests whose author knew to write it.
//
// KeepAwake is deliberately NOT routed through these: it needs a live
// *exec.Cmd to Start, Wait and Kill, and abstracting that would be a bigger
// change than the risk justifies — caffeinate changes nothing a user can see,
// and because it blocks, a test that reached it would hang rather than quietly
// wreck the machine. Route it through here if it ever stops blocking.
var (
	// runCombined runs a one-shot command and returns its combined output.
	runCombined = execCombined

	// runToCompletion runs a command and waits, discarding its output.
	runToCompletion = execToCompletion
)

// The real implementations are named rather than written inline as closures so
// that a test which deliberately wants real execution can opt back into it —
// `runCombined = execCombined` with harmless binaries — instead of being unable
// to reach the code path at all. TestRunSystem does exactly that.

// #nosec G204 -- every caller passes a fixed absolute path and fixed
// arguments; nothing user-supplied reaches exe or args.
func execCombined(exe string, args ...string) ([]byte, error) {
	return exec.Command(exe, args...).CombinedOutput()
}

// #nosec G204 -- exe is a fixed absolute path and each arg is a discrete exec
// argument (no shell), so a URL cannot become a command.
func execToCompletion(exe string, args ...string) error {
	return exec.Command(exe, args...).Run()
}
