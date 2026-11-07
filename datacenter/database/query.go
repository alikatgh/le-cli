package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"
	"unicode/utf8"
)

const maxInput = 64 << 10
const maxResult = 1 << 20
const maxRows = 1000
const queryTimeout = 5 * time.Second

const readNotice = "Read-only adapter; use a least-privilege read-only role. Privileged functions and external side effects are not sandboxed."

type Result struct {
	Engine      string    `json:"engine"`
	DataMode    string    `json:"data_mode"`
	Notice      string    `json:"notice"`
	CollectedAt time.Time `json:"collected_at"`
	Columns     []string  `json:"columns"`
	Rows        [][]any   `json:"rows"`
}

var sqlStart = regexp.MustCompile(`(?i)^(SELECT|WITH|EXPLAIN)\b`)

func validInput(input []byte) error {
	if len(input) > maxInput {
		return ErrLimit
	}
	if len(bytes.TrimSpace(input)) == 0 || !utf8.Valid(input) || bytes.IndexByte(input, 0) >= 0 {
		return ErrConfig
	}
	return nil
}
func validSQL(input []byte) error {
	if err := validInput(input); err != nil {
		return err
	}
	if !sqlStart.Match(bytes.TrimSpace(input)) {
		return ErrConfig
	}
	return nil
}

// Query executes exactly one guarded read request. No result is returned when a
// row/byte limit, cancellation, driver error, or cleanup failure occurs.
func Query(ctx context.Context, c Config, input []byte) (Result, error) {
	if ctx == nil || ValidateConfig(c) != nil {
		return Result{}, ErrConfig
	}
	if err := validInput(input); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var r Result
	var err error
	switch c.Engine {
	case "postgres", "mysql", "mariadb":
		if err = validSQL(input); err == nil {
			r, err = sqlQuery(ctx, c, string(bytes.TrimSpace(input)), nil)
		}
	case "mongodb":
		r, err = mongoRead(ctx, c, input, false)
	case "redis":
		r, err = redisRead(ctx, c, input, false)
	}
	if err != nil {
		return Result{}, safeError(ctx, err)
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return finish(r)
}

// Schema reads fixed metadata queries; it never samples arbitrary documents or
// enumerates every Redis key.
func Schema(ctx context.Context, c Config) (Result, error) {
	if ctx == nil || ValidateConfig(c) != nil {
		return Result{}, ErrConfig
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var r Result
	var err error
	switch c.Engine {
	case "postgres":
		r, err = sqlQuery(ctx, c, `SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns WHERE table_catalog = $1 AND table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name, ordinal_position LIMIT 1001`, []any{c.Database})
	case "mysql", "mariadb":
		r, err = sqlQuery(ctx, c, `SELECT table_schema, table_name, column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = ? ORDER BY table_name, ordinal_position LIMIT 1001`, []any{c.Database})
	case "mongodb":
		r, err = mongoRead(ctx, c, nil, true)
	case "redis":
		r, err = redisRead(ctx, c, nil, true)
	}
	if err != nil {
		return Result{}, safeError(ctx, err)
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return finish(r)
}

func newResult(c Config, columns []string) Result {
	return Result{Engine: c.Engine, DataMode: "live", Notice: readNotice, CollectedAt: time.Now().UTC(), Columns: columns, Rows: [][]any{}}
}
func safeError(ctx context.Context, err error) error {
	if errors.Is(err, ErrConfig) {
		return ErrConfig
	}
	if errors.Is(err, ErrLimit) {
		return ErrLimit
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ErrQuery
}
func finish(r Result) (Result, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return Result{}, ErrQuery
	}
	if len(b) > maxResult {
		return Result{}, ErrLimit
	}
	return r, nil
}

type resultBuilder struct {
	result Result
	size   int
}

func builder(c Config, columns []string) (*resultBuilder, error) {
	r := newResult(c, columns)
	b, err := json.Marshal(r)
	if err != nil {
		return nil, ErrQuery
	}
	if len(columns) > 256 || len(b) > maxResult {
		return nil, ErrLimit
	}
	return &resultBuilder{result: r, size: len(b)}, nil
}
func (b *resultBuilder) add(row []any) error {
	if len(b.result.Rows) >= maxRows {
		return ErrLimit
	}
	if len(row) != len(b.result.Columns) {
		return ErrQuery
	}
	for i, v := range row {
		if raw, ok := v.([]byte); ok {
			row[i] = string(raw)
		}
		if text, ok := row[i].(string); ok && !utf8.ValidString(text) {
			return ErrQuery
		}
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return ErrQuery
	}
	b.size += len(encoded) + 1
	if b.size > maxResult {
		return ErrLimit
	}
	b.result.Rows = append(b.result.Rows, row)
	return nil
}
