package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/logging"
	"github.com/redis/go-redis/v9/maintnotifications"
)

var redisLogOnce sync.Once

func redisArgs(input []byte) ([]string, error) {
	if err := validInput(input); err != nil {
		return nil, err
	}
	var args []string
	d := json.NewDecoder(bytes.NewReader(input))
	if d.Decode(&args) != nil {
		return nil, ErrConfig
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrConfig
	}
	if len(args) == 0 || len(args) > 34 {
		return nil, ErrConfig
	}
	for _, s := range args {
		if !plain(s, maxInput) {
			return nil, ErrConfig
		}
	}
	args[0] = strings.ToUpper(args[0])
	valid := false
	switch args[0] {
	case "PING", "DBSIZE":
		valid = len(args) == 1
	case "GET", "TYPE", "TTL", "HGETALL":
		valid = len(args) == 2
	case "HGET":
		valid = len(args) == 3
	case "MGET", "EXISTS":
		valid = len(args) >= 2 && len(args) <= 33
	case "LRANGE", "ZRANGE":
		if len(args) == 4 {
			start, e1 := strconv.ParseInt(args[2], 10, 32)
			end, e2 := strconv.ParseInt(args[3], 10, 32)
			valid = e1 == nil && e2 == nil && start >= 0 && end >= start && end-start < 100
		}
	case "SCAN":
		if len(args) == 4 && strings.EqualFold(args[2], "COUNT") {
			_, e1 := strconv.ParseUint(args[1], 10, 64)
			count, e2 := strconv.Atoi(args[3])
			valid = e1 == nil && e2 == nil && count >= 1 && count <= 100
			args[2] = "COUNT"
		}
	}
	if !valid {
		return nil, ErrConfig
	}
	return args, nil
}

func redisOptions(c Config) (*redis.Options, error) {
	t, err := TLSConfig(c)
	if err != nil {
		return nil, err
	}
	db, _ := strconv.Atoi(c.Database)
	var attempted atomic.Bool
	options := &redis.Options{Addr: c.Address, Username: c.Username, Password: c.Password, DB: db, Protocol: 2, MaxRetries: -1, DialerRetries: 1, PoolSize: 1, MaxActiveConns: 1, MaxConcurrentDials: 1, MinIdleConns: 0, PipelinePoolSize: -1, DisableIdentity: true, ReadTimeout: queryTimeout, WriteTimeout: queryTimeout, DialTimeout: queryTimeout, PoolTimeout: queryTimeout, ContextTimeoutEnabled: true, MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled}}
	options.Dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		if attempted.Swap(true) {
			return nil, ErrQuery
		}
		conn, err := encryptedDial(ctx, c.Address, t)
		if err != nil {
			return nil, err
		}
		return guardedConn(conn, false), nil
	}
	return options, nil
}

func redisRead(ctx context.Context, c Config, input []byte, schema bool) (out Result, err error) {
	var args []string
	if !schema {
		args, err = redisArgs(input)
		if err != nil {
			return Result{}, err
		}
	}
	o, err := redisOptions(c)
	if err != nil {
		return Result{}, err
	}
	// go-redis's logger is process-wide. This CLI package never permits driver
	// diagnostics containing endpoint, authentication, command or server text.
	redisLogOnce.Do(logging.Disable)
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	client := redis.NewClient(o)
	defer func() {
		if e := client.Close(); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	if schema {
		size, e := client.DBSize(ctx).Result()
		if e != nil {
			return Result{}, e
		}
		info, e := client.Info(ctx, "keyspace").Result()
		if e != nil {
			return Result{}, e
		}
		b, e := builder(c, []string{"metric", "value"})
		if e != nil {
			return Result{}, e
		}
		if e = b.add([]any{"dbsize", size}); e != nil {
			return Result{}, e
		}
		if e = b.add([]any{"keyspace", info}); e != nil {
			return Result{}, e
		}
		return b.result, nil
	}
	if args[0] == "HGETALL" {
		size, e := client.HLen(ctx, args[1]).Result()
		if e != nil {
			return Result{}, e
		}
		if size > maxRows {
			return Result{}, ErrLimit
		}
	}
	command := make([]any, len(args))
	for i, s := range args {
		command[i] = s
	}
	value, err := client.Do(ctx, command...).Result()
	if errors.Is(err, redis.Nil) {
		value = nil
		err = nil
	}
	if err != nil {
		return Result{}, err
	}
	return redisResult(c, args, value)
}

func redisResult(c Config, args []string, value any) (Result, error) {
	columns := []string{"value"}
	if args[0] == "MGET" {
		columns = []string{"key", "value"}
	}
	if args[0] == "HGETALL" {
		columns = []string{"field", "value"}
	}
	if args[0] == "SCAN" {
		columns = []string{"cursor", "key"}
	}
	b, err := builder(c, columns)
	if err != nil {
		return Result{}, err
	}
	array, isArray := value.([]any)
	switch args[0] {
	case "MGET":
		if !isArray || len(array) != len(args)-1 {
			return Result{}, ErrQuery
		}
		for i, v := range array {
			if err = b.add([]any{args[i+1], v}); err != nil {
				return Result{}, err
			}
		}
	case "HGETALL":
		if !isArray || len(array)%2 != 0 {
			return Result{}, ErrQuery
		}
		for i := 0; i < len(array); i += 2 {
			if err = b.add([]any{array[i], array[i+1]}); err != nil {
				return Result{}, err
			}
		}
	case "LRANGE", "ZRANGE":
		if !isArray {
			return Result{}, ErrQuery
		}
		if len(array) > 100 {
			return Result{}, ErrLimit
		}
		for _, v := range array {
			if err = b.add([]any{v}); err != nil {
				return Result{}, err
			}
		}
	case "SCAN":
		if !isArray || len(array) != 2 {
			return Result{}, ErrQuery
		}
		cursor, ok := array[0].(string)
		if !ok {
			return Result{}, ErrQuery
		}
		if _, e := strconv.ParseUint(cursor, 10, 64); e != nil {
			return Result{}, ErrQuery
		}
		keys, ok := array[1].([]any)
		if !ok {
			return Result{}, ErrQuery
		}
		if len(keys) > 100 {
			return Result{}, ErrLimit
		}
		if len(keys) == 0 {
			if err = b.add([]any{cursor, nil}); err != nil {
				return Result{}, err
			}
		}
		for _, key := range keys {
			if err = b.add([]any{cursor, key}); err != nil {
				return Result{}, err
			}
		}
	default:
		if isArray {
			return Result{}, ErrQuery
		}
		if err = b.add([]any{value}); err != nil {
			return Result{}, err
		}
	}
	return b.result, nil
}
