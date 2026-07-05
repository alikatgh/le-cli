package intel

import (
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

// LE-CLI-001: `text` in Make() includes the cwd, so a plain
// strings.Contains(text, "mongod") tagged any project living under a
// "mongodb-*" folder as a MongoDB database. The fix requires "mongod" as a
// bounded word — the real daemon is literally `mongod`, so it still matches.

func TestMakeDoesNotFalsePositiveMongoFromCwd(t *testing.T) {
	l := scan.Listener{
		PID:         1,
		Ports:       []string{"3000"},
		Command:     "node",
		CommandLine: "node /Users/dev/mongodb-dashboard/server.js",
		Cwd:         "/Users/dev/mongodb-dashboard",
	}
	got := Make(l, emptyEnv())
	if got.Identity == "MongoDB" {
		t.Errorf("a Node app in ~/mongodb-dashboard was misclassified as MongoDB (identity=%q, kind=%q)", got.Identity, got.Kind)
	}
}

func TestMakeStillIdentifiesRealMongod(t *testing.T) {
	// Regression guard: word-boundary matching must still catch the actual
	// daemon binary, whose command is literally `mongod`.
	l := scan.Listener{
		PID:         1,
		Ports:       []string{"27017"},
		Command:     "mongod",
		CommandLine: "/usr/local/bin/mongod --dbpath /data/db",
		Cwd:         "/data/db",
	}
	got := Make(l, emptyEnv())
	if got.Identity != "MongoDB" {
		t.Errorf("real mongod identity = %q, want MongoDB", got.Identity)
	}
}
