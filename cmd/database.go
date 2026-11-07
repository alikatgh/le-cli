package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/alikatgh/le-cli/datacenter/database"
	"github.com/alikatgh/le-cli/tools"
	"github.com/spf13/cobra"
)

type databaseOptions struct {
	address, name, collection, ca, output, binary, credentials string
	tls, json, demo                                            bool
	timeout                                                    time.Duration
	maxBytes                                                   int64
}

func databaseCmd() *cobra.Command {
	root := &cobra.Command{Use: "db", Short: "Read databases and create exclusive vendor-format backups", Long: "Explicit single-endpoint database tools; no discovery or background jobs.\nSupported engines: postgres, mysql, mariadb, mongodb, redis.\nQuery input comes from stdin only: one SQL read, Mongo JSON filter, or Redis JSON command array.\nUse least-privilege read-only credentials via LOCALHOST_DB_USERNAME/PASSWORD and Varlock.\nRemote connections require verified TLS. Literal loopback can use plaintext.\nQuery/schema results are limited to 1,000 rows and 1 MiB; requests to 64 KiB.\nPrivileged SQL functions and server-side effects are not sandboxed.\nBackups on macOS/Linux require an independently installed trusted vendor utility and an absolute new output path.\nWindows backups are unavailable pending process-tree containment support.\nMySQL/MariaDB/Mongo backups also need an existing private vendor credentials file.\nRedis backups cover ALL logical databases and may trigger BGSAVE. Restore tests remain your responsibility."}
	for _, action := range []string{"query", "schema", "backup"} {
		action := action
		o := &databaseOptions{}
		c := &cobra.Command{Use: action + " ENGINE", Short: map[string]string{"query": "Run one guarded read from stdin", "schema": "Read fixed schema or keyspace metadata", "backup": "Create a new private, size-limited vendor-format file"}[action], Args: usageArgs(cobra.ExactArgs(1)), ValidArgs: []string{"postgres", "mysql", "mariadb", "mongodb", "redis"}, RunE: func(cmd *cobra.Command, args []string) error { return runDatabase(cmd, action, args[0], o) }}
		f := c.Flags()
		f.StringVar(&o.address, "address", "", "Explicit host:port (or LOCALHOST_DB_ADDRESS)")
		f.StringVar(&o.name, "database", "", "Database name; Redis index 0–15 (or LOCALHOST_DB_DATABASE)")
		f.StringVar(&o.collection, "collection", "", "Mongo collection for query")
		f.StringVar(&o.ca, "ca", "", "CA certificate path; enables verified TLS")
		f.BoolVar(&o.tls, "tls", false, "Require verified TLS even for literal loopback")
		f.BoolVar(&o.json, "json", false, "Structured JSON output")
		if action == "backup" {
			f.StringVar(&o.output, "output", "", "Absolute path for a new private backup; never overwritten")
			f.StringVar(&o.binary, "binary", "", "Absolute path to an operator-trusted vendor dump utility")
			f.StringVar(&o.credentials, "credentials-file", "", "Existing owner-private vendor credentials configuration")
			f.Int64Var(&o.maxBytes, "max-bytes", 64<<20, "Maximum backup bytes (1–268435456); partial files preserved")
			f.DurationVar(&o.timeout, "timeout", time.Minute, "Process deadline (positive, at most 5m); filesystem I/O may outlast it")
		} else {
			f.BoolVar(&o.demo, "demo", false, "Invented offline rows; ignores environment and stdin")
			f.DurationVar(&o.timeout, "timeout", 10*time.Second, "Whole database operation deadline (positive, at most 10s; excludes stdin)")
		}
		root.AddCommand(c)
	}
	return root
}

func runDatabase(cmd *cobra.Command, action, engine string, o *databaseOptions) error {
	switch engine {
	case "postgres", "mysql", "mariadb", "mongodb", "redis":
	default:
		return usage(database.ErrConfig)
	}
	maxTimeout := 10 * time.Second
	if action == "backup" {
		maxTimeout = 5 * time.Minute
	}
	if o.timeout <= 0 || o.timeout > maxTimeout {
		return usage(errors.New("invalid database timeout"))
	}
	if o.demo {
		if o.address != "" || o.name != "" || o.collection != "" || o.ca != "" || o.tls {
			return usage(errors.New("--demo cannot be combined with a live target"))
		}
		data := database.Result{Engine: engine, DataMode: "demo", CollectedAt: time.Now().UTC(), Notice: "STATIC DEMO: invented rows; no database, credentials or stdin were read.", Columns: []string{"example", "value"}, Rows: [][]any{{"demo-record", 42}}}
		if action == "schema" {
			data.Columns = []string{"object", "field", "type"}
			data.Rows = [][]any{{"demo_records", "id", "integer"}}
		}
		return writeData(cmd, data, o.json, func(w io.Writer) { writeDatabaseRows(w, data) })
	}
	address, name := o.address, o.name
	if address == "" {
		address = os.Getenv("LOCALHOST_DB_ADDRESS")
	}
	if name == "" {
		name = os.Getenv("LOCALHOST_DB_DATABASE")
	}
	c := database.Config{Engine: engine, Address: address, Database: name, Collection: o.collection, Username: os.Getenv("LOCALHOST_DB_USERNAME"), Password: os.Getenv("LOCALHOST_DB_PASSWORD"), CAFile: o.ca, TLS: o.tls}
	if database.ValidateConfig(c) != nil {
		return usage(database.ErrConfig)
	}
	var input []byte
	if action == "query" {
		var readErr error
		input, readErr = io.ReadAll(io.LimitReader(cmd.InOrStdin(), (64<<10)+1))
		if readErr != nil {
			return errors.New("cannot read database request from stdin")
		}
		if len(input) > 64<<10 {
			return usage(database.ErrLimit)
		}
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
	defer cancel()
	if action == "backup" {
		data, err := database.Backup(ctx, c, database.BackupOptions{Destination: o.output, Binary: o.binary, CredentialsFile: o.credentials, MaxBytes: o.maxBytes, Timeout: o.timeout})
		// Once a file exists, report its path and completion state even on failure.
		if data.Path != "" {
			if writeErr := writeData(cmd, data, o.json, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "engine=%s complete=%t bytes=%d format=%s\npath=%s\n%s\n", data.Engine, data.Complete, data.Bytes, data.Format, strconv.QuoteToASCII(data.Path), data.Notice)
			}); writeErr != nil {
				return writeErr
			}
		}
		return databaseError(err)
	}
	var data database.Result
	var err error
	if action == "query" {
		data, err = database.Query(ctx, c, input)
	} else {
		data, err = database.Schema(ctx, c)
	}
	if err != nil {
		return databaseError(err)
	}
	return writeData(cmd, data, o.json, func(w io.Writer) { writeDatabaseRows(w, data) })
}

func databaseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, database.ErrConfig) {
		return usage(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("database deadline elapsed: %w", tools.ErrTimeout)
	}
	return err
}
func writeDatabaseRows(w io.Writer, data database.Result) {
	_, _ = fmt.Fprintf(w, "Database %s [%s], collected %s\n%s\n", data.Engine, data.DataMode, data.CollectedAt.Format(time.RFC3339), data.Notice)
	t := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	columns := make([]string, len(data.Columns))
	for i, s := range data.Columns {
		columns[i] = strconv.QuoteToASCII(s)
	}
	_, _ = fmt.Fprintln(t, strings.Join(columns, "\t"))
	for _, row := range data.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			b, err := json.Marshal(v)
			if err != nil {
				cells[i] = "unavailable"
			} else {
				cells[i] = asciiControls(string(b))
			}
		}
		_, _ = fmt.Fprintln(t, strings.Join(cells, "\t"))
	}
	_ = t.Flush()
	_, _ = fmt.Fprintf(w, "%d rows\n", len(data.Rows))
}
func asciiControls(s string) string {
	// JSON already escapes C0 controls. Hide format controls while keeping the
	// ordinary JSON punctuation readable in a terminal table.
	var b strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			q := strconv.QuoteToASCII(string(r))
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
