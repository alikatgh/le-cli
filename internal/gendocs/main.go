// Command gendocs generates le's man pages into man/. Man pages change only
// when a command's flags or usage text changes, so they're committed rather
// than generated fresh on every release — run `go run ./internal/gendocs`
// and commit the diff whenever cmd/ changes.
package main

import (
	"log"
	"os"

	"github.com/spf13/cobra/doc"

	"github.com/alikatgh/le-cli/cmd"
)

func main() {
	const dir = "man"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	header := &doc.GenManHeader{
		Title:   "LE",
		Section: "1",
		Source:  "le",
	}
	if err := doc.GenManTree(cmd.NewRootForDocs(), header, dir); err != nil {
		log.Fatal(err)
	}
}
