package intel

import (
	"testing"

	"github.com/alikatgh/le-cli/scan"
)

// Parity with the mac app (LE-314/316/328): a database is identified by the
// BINARY it runs, not the project folder. A dev server living under a
// DB-named folder must not be classified as that database.

func TestMakeDoesNotClassifyDBFromCwd(t *testing.T) {
	for _, folder := range []string{
		"/Users/dev/postgres-migration",
		"/Users/dev/redis-cache",
		"/Users/dev/mysql-backup",
	} {
		l := scan.Listener{
			PID:         1,
			Ports:       []string{"3000"},
			Command:     "node",
			CommandLine: "node /Users/dev/app/server.js",
			Cwd:         folder,
		}
		if got := Make(l, emptyEnv()); got.Kind == Database {
			t.Errorf("%s misclassified as Database (identity=%q)", folder, got.Identity)
		}
	}
}

func TestMakeStillIdentifiesRealPostgres(t *testing.T) {
	l := scan.Listener{
		PID:         1,
		Ports:       []string{"5432"},
		Command:     "postgres",
		CommandLine: "/opt/homebrew/opt/postgresql@14/bin/postgres -D /var/pg",
		Cwd:         "/Users/dev/some-app",
	}
	if got := Make(l, emptyEnv()); got.Identity != "Postgres" {
		t.Errorf("real postgres identity = %q, want Postgres", got.Identity)
	}
}
