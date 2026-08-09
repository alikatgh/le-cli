// Command gendocs generates le's man pages into man/. Man pages change only
// when a command's flags or usage text changes, so they're committed rather
// than generated fresh on every release — run `go run ./internal/gendocs`
// and commit the diff whenever cmd/ changes.
//
// CI runs this and fails on a dirty man/ (see .github/workflows/ci.yml), which
// is the only thing that keeps the committed pages honest: the manual "run it
// and commit the diff" step is exactly what got skipped for twelve commands.
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra/doc"

	"github.com/alikatgh/le-cli/cmd"
)

// manDate stamps the .TH header and the HISTORY line. It is PINNED rather than
// time.Now(): with a live clock every generated page differs from the committed
// one the moment the month rolls over, so the CI drift check would go red on
// the 1st of every month for a repo nobody touched. Bump it deliberately when
// the pages get a real overhaul; drift then means content drift, nothing else.
const manDate = "2026-08-09"

func main() {
	const dir = "man"
	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Fatal(err)
	}
	// Clear the directory first: GenManTree writes and overwrites, but never
	// DELETES, so a renamed or removed command leaves its old page behind —
	// tracked, unmodified, and therefore invisible to a `git diff` drift check.
	// Regenerating from empty makes the removal show up as a deletion.
	stale, err := filepath.Glob(filepath.Join(dir, "*.1"))
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range stale {
		if err := os.Remove(f); err != nil {
			log.Fatal(err)
		}
	}
	date, err := time.Parse(time.DateOnly, manDate)
	if err != nil {
		log.Fatal(err)
	}
	header := &doc.GenManHeader{
		Title:   "LE",
		Section: "1",
		Source:  "le",
		Date:    &date,
	}
	if err := doc.GenManTree(cmd.NewRootForDocs(), header, dir); err != nil {
		log.Fatal(err)
	}
}
