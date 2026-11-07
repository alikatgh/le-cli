package database

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

func sqlQuery(ctx context.Context, c Config, input string, args []any) (Result, error) {
	if c.Engine == "postgres" {
		return postgresQuery(ctx, c, input, args)
	}
	return mysqlQuery(ctx, c, input, args)
}

func postgresConfig(c Config) (*pgx.ConnConfig, error) {
	// pgx offers no environment-free constructor. Its service key triggers a
	// file read even when overridden with an empty string, so fail closed when
	// that ambient feature is set rather than reading an unrelated service file.
	if os.Getenv("PGSERVICE") != "" {
		return nil, ErrConfig
	}
	// Explicit harmless parser values prevent ambient service/passfile/client-key
	// loading. All operational values below come only from the caller's Config.
	p, err := pgx.ParseConfig(`host=127.0.0.1 port=5432 user=le_cli database=le_cli password=unused servicefile='' passfile='' sslmode=disable sslcert='' sslkey='' sslrootcert='' sslpassword='' target_session_attrs=any sslnegotiation=postgres min_protocol_version=3.0 max_protocol_version=3.0 channel_binding=prefer require_auth='password,md5,scram-sha-256,none' connect_timeout=5`)
	if err != nil {
		return nil, ErrConfig
	}
	t, err := TLSConfig(c)
	if err != nil {
		return nil, err
	}
	h, port, _ := net.SplitHostPort(c.Address)
	n, parseErr := strconv.ParseUint(port, 10, 16)
	if parseErr != nil || n == 0 {
		return nil, ErrConfig
	}
	p.Host = h
	p.Port = uint16(n)
	p.User = c.Username
	p.Password = c.Password
	p.Database = c.Database
	p.TLSConfig = t
	p.Fallbacks = nil
	p.ConnectTimeout = queryTimeout
	p.RuntimeParams = map[string]string{"statement_timeout": "5000", "default_transaction_read_only": "on", "application_name": "le-cli"}
	p.ValidateConnect = nil
	p.AfterConnect = nil
	p.AfterNetConnect = nil
	p.OnNotice = nil
	p.OnNotification = nil
	p.OAuthTokenProvider = nil
	p.KerberosSpn = ""
	p.KerberosSrvName = ""
	p.RequireAuth = "password,md5,scram-sha-256,none"
	p.SSLNegotiation = "postgres"
	p.ChannelBinding = "prefer"
	p.MinProtocolVersion = "3.0"
	p.MaxProtocolVersion = "3.0"
	p.MaxProtocolMessageBodyLen = maxResult
	p.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	p.StatementCacheCapacity = 0
	p.DescriptionCacheCapacity = 0
	var attempted atomic.Bool
	p.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		if attempted.Swap(true) {
			return nil, ErrQuery
		}
		return boundedDial(ctx, c.Address)
	}
	p.LookupFunc = func(ctx context.Context, _ string) ([]string, error) { return []string{h}, nil }
	return p, nil
}

func postgresQuery(ctx context.Context, c Config, input string, args []any) (out Result, err error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	config, err := postgresConfig(c)
	if err != nil {
		return Result{}, err
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if e := conn.Close(closeCtx); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Result{}, err
	}
	defer func() {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), time.Second)
		defer rollbackCancel()
		if e := tx.Rollback(rollbackCtx); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	statement, err := tx.Prepare(ctx, "le_cli_read", input)
	if err != nil {
		return Result{}, err
	}
	rows, err := tx.Query(ctx, statement.Name, args...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	columns := make([]string, len(fields))
	for i, f := range fields {
		columns[i] = f.Name
	}
	b, err := builder(c, columns)
	if err != nil {
		return Result{}, err
	}
	for rows.Next() {
		values, e := rows.Values()
		if e != nil {
			return Result{}, e
		}
		if e = b.add(values); e != nil {
			return Result{}, e
		}
	}
	if err = rows.Err(); err != nil {
		return Result{}, err
	}
	return b.result, nil
}

type discardMySQLLog struct{}

func (discardMySQLLog) Print(...any) {}

func mysqlConfig(c Config) (*mysql.Config, error) {
	t, err := TLSConfig(c)
	if err != nil {
		return nil, err
	}
	m := mysql.NewConfig()
	m.User = c.Username
	m.Passwd = c.Password
	m.Net = "tcp"
	m.Addr = c.Address
	m.DBName = c.Database
	m.TLS = t
	m.Timeout = queryTimeout
	m.ReadTimeout = queryTimeout
	m.WriteTimeout = queryTimeout
	m.MaxAllowedPacket = maxResult
	m.AllowAllFiles = false
	m.AllowCleartextPasswords = false
	m.AllowFallbackToPlaintext = false
	m.AllowOldPasswords = false
	m.MultiStatements = false
	m.InterpolateParams = false
	m.CheckConnLiveness = false
	m.ParseTime = true
	m.Logger = discardMySQLLog{}
	var attempted atomic.Bool
	m.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		if attempted.Swap(true) {
			return nil, ErrQuery
		}
		return boundedDial(ctx, c.Address)
	}
	return m, nil
}

func mysqlQuery(ctx context.Context, c Config, input string, args []any) (Result, error) {
	m, err := mysqlConfig(c)
	if err != nil {
		return Result{}, err
	}
	connector, err := mysql.NewConnector(m)
	if err != nil {
		return Result{}, err
	}
	db := sql.OpenDB(connector)
	result, err := mysqlReadDB(ctx, c, input, args, db)
	if errors.Is(err, mysql.ErrMetadataLimit) {
		return Result{}, ErrLimit
	}
	return result, err
}

// A separate function permits a driver-hook fixture to verify the transaction
// and prepared-statement journey without a real database or extra dependency.
func mysqlReadDB(ctx context.Context, c Config, input string, args []any, db *sql.DB) (out Result, err error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(queryTimeout)
	defer func() {
		if e := db.Close(); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	conn, err := db.Conn(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if e := conn.Close(); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	setting := "SET SESSION max_execution_time = 5000"
	if c.Engine == "mariadb" {
		setting = "SET SESSION max_statement_time = 5"
	}
	if _, err = conn.ExecContext(ctx, setting); err != nil {
		return Result{}, err
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if e := tx.Rollback(); e != nil && e != sql.ErrTxDone && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	statement, err := tx.PrepareContext(ctx, input)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if e := statement.Close(); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	rows, err := statement.QueryContext(ctx, args...)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if e := rows.Close(); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return Result{}, err
	}
	b, err := builder(c, columns)
	if err != nil {
		return Result{}, err
	}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return Result{}, err
		}
		if err = b.add(values); err != nil {
			return Result{}, err
		}
	}
	if err = rows.Err(); err != nil {
		return Result{}, err
	}
	if rows.NextResultSet() {
		return Result{}, ErrQuery
	}
	if err = rows.Err(); err != nil {
		return Result{}, err
	}
	if err = rows.Close(); err != nil {
		return Result{}, err
	}
	return b.result, nil
}
