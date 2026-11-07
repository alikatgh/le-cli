package database

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

func fixtureConfig(engine string) Config {
	db := "fixture"
	if engine == "redis" {
		db = "0"
	}
	return Config{Engine: engine, Address: "127.0.0.1:12345", Database: db, Username: "fixture-user", Password: "fixture-password"}
}

func TestConfigAndTLSBoundaries(t *testing.T) {
	for _, engine := range []string{"postgres", "mysql", "mariadb", "mongodb", "redis"} {
		c := fixtureConfig(engine)
		if err := ValidateConfig(c); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(c)
		if string(encoded) != "{}" {
			t.Fatal("configuration serialized credentials")
		}
		local, err := TLSConfig(c)
		if err != nil || local != nil {
			t.Fatal("literal loopback plaintext rejected")
		}
		c.Address = "db.example.test:1234"
		remote, err := TLSConfig(c)
		if err != nil || remote == nil || remote.InsecureSkipVerify || remote.ServerName != "db.example.test" {
			t.Fatal("remote verified TLS missing")
		}
		c.Address = "localhost:1234"
		remote, err = TLSConfig(c)
		if err != nil || remote == nil {
			t.Fatal("DNS loopback incorrectly permits plaintext")
		}
	}
	for _, address := range []string{"", "localhost", "postgres://user:fixture-password@host/db", "user@host:1234", "/tmp/socket", "host:0", "host:65536", "a,b:1234", "host:1234?x", "bad\n.example:1234", "[fe80::1%en0]:1234"} {
		c := fixtureConfig("postgres")
		c.Address = address
		if !errors.Is(ValidateConfig(c), ErrConfig) {
			t.Errorf("accepted unsafe endpoint %q", address)
		}
	}
	c := fixtureConfig("redis")
	for _, db := range []string{"-1", "16", "01", "not-a-number"} {
		c.Database = db
		if !errors.Is(ValidateConfig(c), ErrConfig) {
			t.Error("accepted invalid Redis DB")
		}
	}
	c = fixtureConfig("postgres")
	c.CAFile = "/does-not-exist/fixture-ca"
	_, err := TLSConfig(c)
	if err == nil || strings.Contains(err.Error(), c.CAFile) || strings.Contains(err.Error(), "fixture-password") {
		t.Fatal("CA failure leaked configuration")
	}
}

func TestInputAndResultLimits(t *testing.T) {
	for _, q := range []string{"SELECT 1", " with x as (select 1) select * from x", "EXPLAIN SELECT 1", "SELECT '; literal'"} {
		if validSQL([]byte(q)) != nil {
			t.Errorf("rejected read form %q", q)
		}
	}
	for _, q := range []string{"INSERT INTO x VALUES(1)", "\\copy x to '/tmp/out'", "COPY x TO STDOUT", "-- comment\nDELETE FROM x", "SELECTED 1", "\x00SELECT 1"} {
		if validSQL([]byte(q)) == nil {
			t.Error("accepted non-read leading form")
		}
	}
	if !errors.Is(validInput([]byte(strings.Repeat("x", maxInput+1))), ErrLimit) {
		t.Fatal("input limit absent")
	}
	b, _ := builder(fixtureConfig("postgres"), []string{"v"})
	for i := 0; i < maxRows; i++ {
		if err := b.add([]any{i}); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(b.add([]any{1}), ErrLimit) {
		t.Fatal("row limit absent")
	}
	b, _ = builder(fixtureConfig("postgres"), []string{"v"})
	if !errors.Is(b.add([]any{strings.Repeat("x", maxResult)}), ErrLimit) {
		t.Fatal("encoded byte limit absent")
	}
	b, _ = builder(fixtureConfig("postgres"), []string{"v"})
	if err := b.add([]any{[]byte("text")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.result.Rows[0][0].(string); !ok {
		t.Fatal("SQL bytes became opaque base64")
	}
	for _, err := range []error{errors.New("fixture-password SELECT secret FROM fixture"), fmt.Errorf("endpoint %s", fixtureConfig("postgres").Address)} {
		safe := safeError(context.Background(), err)
		if safe != ErrQuery {
			t.Fatal("driver failure was exposed")
		}
	}
}

func TestDriverConfigurationIsolation(t *testing.T) {
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGPASSFILE", "/not-a-real-fixture-passfile")
	t.Setenv("PGSSLCERT", "/not-a-real-fixture-client-cert")
	t.Setenv("PGSSLKEY", "/not-a-real-fixture-client-key")
	t.Setenv("PGREQUIREAUTH", "invalid-ambient-auth")
	p, err := postgresConfig(fixtureConfig("postgres"))
	if err != nil {
		t.Fatalf("explicit config inherited environment: %v", err)
	}
	if len(p.Fallbacks) != 0 || p.MaxProtocolMessageBodyLen != maxResult || p.RuntimeParams["statement_timeout"] != "5000" || p.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Fatal("PostgreSQL bounds missing")
	}
	t.Setenv("PGSERVICE", "fixture-service")
	if _, err := postgresConfig(fixtureConfig("postgres")); !errors.Is(err, ErrConfig) {
		t.Fatal("ambient service file not blocked")
	}
	m, err := mongoOptions(fixtureConfig("mongodb"))
	if err != nil || m.Direct == nil || !*m.Direct || m.MaxPoolSize == nil || *m.MaxPoolSize != 1 || m.RetryReads == nil || *m.RetryReads || m.RetryWrites == nil || *m.RetryWrites {
		t.Fatal("MongoDB discovery/retries/pool not bounded")
	}
	r, err := redisOptions(fixtureConfig("redis"))
	if err != nil || r.Protocol != 2 || r.MaxRetries != -1 || r.PoolSize != 1 || r.MaxActiveConns != 1 || r.PipelinePoolSize != -1 || !r.DisableIdentity {
		t.Fatal("Redis retries/protocol/pool not bounded")
	}
}

func TestMongoEndpointCanonicalizationStaysWithinConfiguredEndpoint(t *testing.T) {
	for _, tc := range []struct {
		requested, configured string
		same                  bool
	}{
		{"db.example.test:27017", "DB.Example.Test:27017", true},
		{"[2001:db8::1]:27017", "[2001:0DB8:0:0:0:0:0:1]:27017", true},
		{"[::ffff:127.0.0.1]:27017", "127.0.0.1:27017", true},
		{"db.example.test:27017", "db.example.test:027017", true},
		{"other.example.test:27017", "db.example.test:27017", false},
		{"db.example.test:27018", "db.example.test:27017", false},
		{"127.0.0.1:27017", "localhost:27017", false},
		{"[fe80::1%en0]:27017", "[fe80::1%en0]:27017", false},
		{"db.example.test:0", "db.example.test:0", false},
	} {
		if sameMongoEndpoint(tc.requested, tc.configured) != tc.same {
			t.Fatalf("endpoint identity changed: %q vs %q", tc.requested, tc.configured)
		}
	}
}

func TestMongoFilterRejectsExecutableOperators(t *testing.T) {
	for _, filter := range []string{`{}`, `{"status":"active","age":{"$gte":18}}`, `{"$and":[{"x":1},{"_id":{"$oid":"507f1f77bcf86cd799439011"}}]}`} {
		if _, err := mongoFilter([]byte(filter)); err != nil {
			t.Errorf("valid filter: %v", err)
		}
	}
	for _, filter := range []string{`[]`, `{"$where":"function(){return true}"}`, `{"a":{"$function":{"body":"script"}}}`, `{"a":{"$code":"script"}}`, `{"a":{"$where":"script"},"a":1}`, `{"$expr":{"$function":{}}}`, `{} {}`, `{"a":` + strings.Repeat("[", 34) + "1" + strings.Repeat("]", 34) + "}"} {
		if _, err := mongoFilter([]byte(filter)); err == nil {
			t.Errorf("accepted executable/deep filter %s", filter)
		}
	}
}

func TestRedisAllowlistAndBounds(t *testing.T) {
	for _, input := range []string{`["GET","key"]`, `["MGET","a","b"]`, `["HGETALL","h"]`, `["LRANGE","list","0","99"]`, `["SCAN","0","COUNT","100"]`, `["PING"]`} {
		if _, err := redisArgs([]byte(input)); err != nil {
			t.Errorf("valid command: %v", err)
		}
	}
	for _, input := range []string{`["SET","k","v"]`, `["EVAL","script","0"]`, `["KEYS","*"]`, `["CONFIG","GET","*"]`, `["GET","k","extra"]`, `["LRANGE","l","0","-1"]`, `["ZRANGE","z","0","100"]`, `["SCAN","0","COUNT","101"]`, `["SCAN","0"]`, `["SCAN","0","MATCH","*"]`, `["PING","payload"]`, `["GET",1]`} {
		if _, err := redisArgs([]byte(input)); err == nil {
			t.Errorf("accepted unsupported command %s", input)
		}
	}
	args := append([]string{"MGET"}, make([]string, 33)...)
	raw, _ := json.Marshal(args)
	if _, err := redisArgs(raw); err == nil {
		t.Fatal("MGET exceeded 32 keys")
	}
	if _, err := redisResult(fixtureConfig("redis"), []string{"HGETALL", "h"}, []any{"orphan"}); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestWireGuardsRejectAnnouncedAllocationBeforeBody(t *testing.T) {
	for _, frame := range []string{"$999999999999\r\n", "*9999999999\r\n", strings.Repeat("*1\r\n", 34) + "+ok\r\n"} {
		if _, err := respFrame(bufio.NewReader(strings.NewReader(frame)), 0, maxResult); !errors.Is(err, ErrLimit) {
			t.Fatalf("unsafe RESP announcement: %v", err)
		}
	}
	reader, writer := net.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	guard := guardedConn(reader, true)
	go func() {
		header := make([]byte, 16)
		binary.LittleEndian.PutUint32(header, uint32(maxResult+1))
		_, _ = writer.Write(header)
	}()
	if _, err := guard.Read(make([]byte, 16)); !errors.Is(err, ErrLimit) {
		t.Fatalf("unsafe Mongo frame: %v", err)
	}
}

type sqlFixture struct {
	events    []string
	readOnly  bool
	rows      int
	closed    bool
	fail      bool
	closeFail bool
}

func TestRedisRESP2ReadAndBoundedCancellation(t *testing.T) {
	for _, mode := range []string{"value", "oversized", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			done := make(chan []string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- nil
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(conn)
				seen := []string{}
				for {
					args, err := readFixtureRedis(reader)
					if err != nil {
						done <- seen
						return
					}
					command := strings.ToUpper(args[0])
					seen = append(seen, command)
					switch command {
					case "HELLO":
						if len(args) < 2 || args[1] != "2" {
							done <- nil
							return
						}
						_, _ = fmt.Fprint(conn, "*0\r\n")
					case "GET":
						switch mode {
						case "value":
							_, _ = fmt.Fprint(conn, "$7\r\nfixture\r\n")
						case "oversized":
							_, _ = fmt.Fprint(conn, "$999999999999\r\n")
						case "cancel": // Wait for the client to close on its deadline.
						}
					default:
						done <- nil
						return
					}
				}
			}()
			c := fixtureConfig("redis")
			c.Address = listener.Addr().String()
			ctx := context.Background()
			var cancel context.CancelFunc
			if mode == "cancel" {
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			}
			r, err := Query(ctx, c, []byte(`["GET","key"]`))
			if mode == "value" {
				if err != nil || len(r.Rows) != 1 || r.Rows[0][0] != "fixture" {
					t.Fatalf("RESP2 result: %+v %v", r, err)
				}
			} else if err == nil || len(r.Rows) > 0 {
				t.Fatal("failed read returned partial data")
			}
			if mode == "cancel" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cancellation: %v", err)
			}
			select {
			case seen := <-done:
				if !reflect.DeepEqual(seen, []string{"HELLO", "GET"}) {
					t.Fatalf("unexpected protocol journey: %v", seen)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Redis connection not closed")
			}
		})
	}
}

func readFixtureRedis(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 4 || line[0] != '*' {
		return nil, io.ErrUnexpectedEOF
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || n < 1 || n > 40 {
		return nil, io.ErrUnexpectedEOF
	}
	args := make([]string, n)
	for i := range args {
		line, err = reader.ReadString('\n')
		if err != nil || len(line) < 4 || line[0] != '$' {
			return nil, io.ErrUnexpectedEOF
		}
		size, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || size < 0 || size > maxInput {
			return nil, io.ErrUnexpectedEOF
		}
		data := make([]byte, size+2)
		if _, err = io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		args[i] = string(data[:size])
	}
	return args, nil
}

type fixtureConnector struct{ s *sqlFixture }

func (f fixtureConnector) Connect(context.Context) (driver.Conn, error) {
	return &fixtureSQLConn{s: f.s}, nil
}
func (f fixtureConnector) Driver() driver.Driver { return fixtureDriver(f) }

type fixtureDriver struct{ s *sqlFixture }

func (f fixtureDriver) Open(string) (driver.Conn, error) { return &fixtureSQLConn{s: f.s}, nil }

type fixtureSQLConn struct{ s *sqlFixture }

func (c *fixtureSQLConn) Prepare(query string) (driver.Stmt, error) {
	c.s.events = append(c.s.events, "prepare:"+query)
	return &fixtureSQLStmt{s: c.s}, nil
}
func (c *fixtureSQLConn) Close() error { c.s.closed = true; return nil }
func (c *fixtureSQLConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unbounded legacy transaction")
}
func (c *fixtureSQLConn) BeginTx(_ context.Context, o driver.TxOptions) (driver.Tx, error) {
	c.s.readOnly = o.ReadOnly
	c.s.events = append(c.s.events, "begin")
	return fixtureSQLTx{c.s}, nil
}
func (c *fixtureSQLConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	c.s.events = append(c.s.events, q)
	return driver.RowsAffected(0), nil
}

type fixtureSQLTx struct{ s *sqlFixture }

func (t fixtureSQLTx) Commit() error   { t.s.events = append(t.s.events, "COMMIT"); return nil }
func (t fixtureSQLTx) Rollback() error { t.s.events = append(t.s.events, "rollback"); return nil }

type fixtureSQLStmt struct{ s *sqlFixture }

func (s *fixtureSQLStmt) Close() error {
	s.s.events = append(s.s.events, "statement-close")
	if s.s.closeFail {
		return errors.New("synthetic statement cleanup failure")
	}
	return nil
}
func (s *fixtureSQLStmt) NumInput() int { return -1 }
func (s *fixtureSQLStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("unexpected write execution")
}
func (s *fixtureSQLStmt) Query([]driver.Value) (driver.Rows, error) {
	s.s.events = append(s.s.events, "prepared-query")
	if s.s.fail {
		return nil, errors.New("fixture-password server secret")
	}
	return &fixtureSQLRows{remaining: s.s.rows}, nil
}

type fixtureSQLRows struct{ remaining int }

func (r *fixtureSQLRows) Columns() []string { return []string{"value"} }
func (r *fixtureSQLRows) Close() error      { return nil }
func (r *fixtureSQLRows) Next(dest []driver.Value) error {
	if r.remaining == 0 {
		return io.EOF
	}
	r.remaining--
	dest[0] = "fixture-value"
	return nil
}

func TestMySQLPreparedReadOnlyJourneyAndCleanup(t *testing.T) {
	for _, engine := range []string{"mysql", "mariadb"} {
		s := &sqlFixture{rows: 1}
		db := sql.OpenDB(fixtureConnector{s})
		r, err := mysqlReadDB(context.Background(), fixtureConfig(engine), "SELECT 'fixture'", nil, db)
		if err != nil || len(r.Rows) != 1 {
			t.Fatalf("query: %+v %v", r, err)
		}
		if !s.readOnly || !s.closed {
			t.Fatal("read-only transaction or connection cleanup absent")
		}
		wantSetting := "SET SESSION max_execution_time = 5000"
		if engine == "mariadb" {
			wantSetting = "SET SESSION max_statement_time = 5"
		}
		want := []string{wantSetting, "begin", "prepare:SELECT 'fixture'", "prepared-query", "statement-close", "rollback"}
		if !reflect.DeepEqual(s.events, want) {
			t.Fatalf("prepared journey %v", s.events)
		}
	}
	s := &sqlFixture{rows: 1001}
	_, err := mysqlReadDB(context.Background(), fixtureConfig("mysql"), "SELECT 1", nil, sql.OpenDB(fixtureConnector{s}))
	if !errors.Is(err, ErrLimit) || !s.closed || s.events[len(s.events)-1] != "rollback" {
		t.Fatalf("limit cleanup: %v %v", err, s.events)
	}
	c := fixtureConfig("mysql")
	cfg, err := mysqlConfig(c)
	if err != nil || cfg.MultiStatements || cfg.InterpolateParams || cfg.AllowAllFiles || cfg.AllowFallbackToPlaintext || cfg.ReadTimeout != queryTimeout {
		t.Fatal("unsafe MySQL driver options")
	}
}

func TestMySQLCleanupFailureDoesNotReturnSuccessfulRows(t *testing.T) {
	s := &sqlFixture{rows: 1, closeFail: true}
	r, err := mysqlReadDB(context.Background(), fixtureConfig("mysql"), "SELECT 1", nil, sql.OpenDB(fixtureConnector{s}))
	if !errors.Is(err, ErrQuery) || len(r.Rows) != 0 || !s.closed {
		t.Fatal("cleanup failure retained a successful result")
	}
}

func TestPostgresExtendedPreparedReadOnlyWireJourney(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	events := make(chan []string, 1)
	failures := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			failures <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		backend := pgproto3.NewBackend(conn, conn)
		startup, err := backend.ReceiveStartupMessage()
		if err != nil {
			failures <- err
			return
		}
		message, ok := startup.(*pgproto3.StartupMessage)
		if !ok || message.Parameters["statement_timeout"] != "5000" || message.Parameters["default_transaction_read_only"] != "on" {
			failures <- errors.New("startup lacks read limits")
			return
		}
		backend.Send(&pgproto3.AuthenticationOk{})
		backend.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "18.0"})
		backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		_ = backend.Flush()
		var seen []string
		for {
			msg, err := backend.Receive()
			if err != nil {
				events <- seen
				return
			}
			switch m := msg.(type) {
			case *pgproto3.Query:
				seen = append(seen, "simple:"+m.String)
				backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("OK")})
				backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'T'})
			case *pgproto3.Parse:
				seen = append(seen, "parse:"+m.Query)
				backend.Send(&pgproto3.ParseComplete{})
			case *pgproto3.Describe:
				if m.ObjectType == 'S' {
					backend.Send(&pgproto3.ParameterDescription{ParameterOIDs: []uint32{}})
				}
				backend.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("value"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1}}})
			case *pgproto3.Bind:
				seen = append(seen, "bind")
				backend.Send(&pgproto3.BindComplete{})
			case *pgproto3.Execute:
				seen = append(seen, "execute")
				backend.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("fixture-value")}})
				backend.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
			case *pgproto3.Sync:
				backend.Send(&pgproto3.ReadyForQuery{TxStatus: 'T'})
			case *pgproto3.Terminate:
				events <- seen
				return
			}
			if err := backend.Flush(); err != nil {
				events <- seen
				return
			}
		}
	}()
	c := fixtureConfig("postgres")
	c.Address = listener.Addr().String()
	result, err := Query(context.Background(), c, []byte("SELECT 'fixture-value'"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0][0] != "fixture-value" {
		t.Fatalf("wire result %+v", result)
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	case seen := <-events:
		joined := strings.Join(seen, "|")
		if !strings.Contains(joined, "simple:begin read only") || !strings.Contains(joined, "parse:SELECT 'fixture-value'") || !strings.Contains(joined, "bind|execute") || !strings.Contains(joined, "simple:rollback") {
			t.Fatalf("wire journey %v", seen)
		}
		if strings.Contains(joined, "simple:SELECT") {
			t.Fatal("user query used simple protocol")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("connection cleanup timed out")
	}
}
