package database

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func mongoFilter(input []byte) (bson.D, error) {
	if err := validInput(input); err != nil {
		return nil, err
	}
	if !uniqueJSON(input) {
		return nil, ErrConfig
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(input))
	d.UseNumber()
	if d.Decode(&value) != nil {
		return nil, ErrConfig
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrConfig
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, ErrConfig
	}
	if !filterNode(value, 0) {
		return nil, ErrConfig
	}
	var filter bson.D
	if bson.UnmarshalExtJSON(input, false, &filter) != nil {
		return nil, ErrConfig
	}
	return filter, nil
}

func uniqueJSON(input []byte) bool {
	d := json.NewDecoder(bytes.NewReader(input))
	d.UseNumber()
	if !jsonNode(d, 0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
func jsonNode(d *json.Decoder, depth int) bool {
	if depth > 32 {
		return false
	}
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return false
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return false
			}
			seen[s] = true
			if !jsonNode(d, depth+1) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for d.More() {
			if !jsonNode(d, depth+1) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func filterNode(v any, depth int) bool {
	if depth > 32 {
		return false
	}
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			if strings.HasPrefix(key, "$") {
				switch key {
				case "$eq", "$ne", "$gt", "$gte", "$lt", "$lte", "$in", "$nin", "$and", "$or", "$nor", "$not", "$exists", "$type", "$regex", "$options", "$elemMatch", "$size", "$all", "$oid", "$date", "$numberInt", "$numberLong", "$numberDouble", "$numberDecimal":
				default:
					return false
				}
			}
			if !filterNode(value, depth+1) {
				return false
			}
		}
	case []any:
		if len(x) > maxRows {
			return false
		}
		for _, value := range x {
			if !filterNode(value, depth+1) {
				return false
			}
		}
	}
	return true
}

type mongoDialer struct {
	config   Config
	attempts atomic.Int32
}

func (d *mongoDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !sameMongoEndpoint(address, d.config.Address) {
		return nil, ErrConfig
	}
	// Poll monitoring uses one socket, the application pool at most one more.
	// No third network attempt is permitted, including monitor reconnects.
	if d.attempts.Add(1) > 2 {
		return nil, ErrQuery
	}
	t, err := TLSConfig(d.config)
	if err != nil {
		return nil, err
	}
	conn, err := encryptedDial(ctx, d.config.Address, t)
	if err != nil {
		return nil, err
	}
	return guardedConn(conn, true), nil
}

// Driver canonicalization may lowercase DNS names and compress IPv6. Compare
// only textual DNS identity or equal literal IPs and the same numeric port;
// never resolve another hostname or accept a discovered endpoint.
func sameMongoEndpoint(requested, configured string) bool {
	rh, rp, rerr := net.SplitHostPort(requested)
	ch, cp, cerr := net.SplitHostPort(configured)
	if rerr != nil || cerr != nil || rh == "" || ch == "" || strings.ContainsAny(rh+ch, "%[] ") {
		return false
	}
	rport, rerr := strconv.ParseUint(rp, 10, 16)
	cport, cerr := strconv.ParseUint(cp, 10, 16)
	if rerr != nil || cerr != nil || rport == 0 || rport != cport {
		return false
	}
	rip, cip := net.ParseIP(rh), net.ParseIP(ch)
	if rip != nil || cip != nil {
		return rip != nil && cip != nil && rip.Equal(cip)
	}
	return strings.EqualFold(rh, ch)
}

type discardMongoLog struct{}

func (discardMongoLog) Info(int, string, ...any)    {}
func (discardMongoLog) Error(error, string, ...any) {}

func mongoOptions(c Config) (*options.ClientOptions, error) {
	if _, err := TLSConfig(c); err != nil {
		return nil, err
	}
	o := options.Client().SetHosts([]string{c.Address}).SetDirect(true).SetMaxPoolSize(1).SetMinPoolSize(0).SetMaxConnecting(1).SetRetryReads(false).SetRetryWrites(false).SetServerSelectionTimeout(queryTimeout).SetConnectTimeout(queryTimeout).SetTimeout(queryTimeout).SetServerMonitoringMode("poll").SetHeartbeatInterval(time.Hour).SetDialer(&mongoDialer{config: c}).SetCompressors([]string{}).SetLoggerOptions(options.Logger().SetSink(discardMongoLog{}))
	if c.Username != "" || c.Password != "" {
		o.SetAuth(options.Credential{AuthMechanism: "SCRAM-SHA-256", AuthSource: c.Database, Username: c.Username, Password: c.Password})
	}
	return o, nil
}

func mongoRead(ctx context.Context, c Config, input []byte, schema bool) (out Result, err error) {
	var filter bson.D
	if !schema {
		if c.Collection == "" || strings.HasPrefix(c.Collection, "$") {
			return Result{}, ErrConfig
		}
		filter, err = mongoFilter(input)
		if err != nil {
			return Result{}, err
		}
	}
	o, err := mongoOptions(c)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	client, err := mongo.Connect(o)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if e := client.Disconnect(closeCtx); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	var cursor *mongo.Cursor
	columns := []string{"document"}
	if schema {
		// nameOnly returns collection names/types, excluding validators, options
		// and stored user documents.
		cursor, err = client.Database(c.Database).ListCollections(ctx, bson.D{}, options.ListCollections().SetNameOnly(true).SetAuthorizedCollections(true).SetBatchSize(32))
		columns = []string{"name", "type"}
	} else {
		cursor, err = client.Database(c.Database).Collection(c.Collection).Find(ctx, filter, options.Find().SetLimit(maxRows+1).SetBatchSize(32))
	}
	if err != nil {
		return Result{}, err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
		defer closeCancel()
		if e := cursor.Close(closeCtx); e != nil && err == nil {
			out = Result{}
			err = ErrQuery
		}
	}()
	b, err := builder(c, columns)
	if err != nil {
		return Result{}, err
	}
	for cursor.Next(ctx) {
		if len(cursor.Current) > maxResult {
			return Result{}, ErrLimit
		}
		if schema {
			var metadata struct {
				Name string `bson:"name"`
				Type string `bson:"type"`
			}
			if err = cursor.Decode(&metadata); err != nil {
				return Result{}, err
			}
			if err = b.add([]any{metadata.Name, metadata.Type}); err != nil {
				return Result{}, err
			}
		} else {
			doc, e := bson.MarshalExtJSON(cursor.Current, false, false)
			if e != nil {
				return Result{}, e
			}
			if !json.Valid(doc) {
				return Result{}, ErrQuery
			}
			if err = b.add([]any{json.RawMessage(doc)}); err != nil {
				return Result{}, err
			}
		}
	}
	if err = cursor.Err(); err != nil {
		return Result{}, err
	}
	return b.result, nil
}
