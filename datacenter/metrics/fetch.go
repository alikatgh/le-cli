package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alikatgh/le-cli/internal/readapi"
)

var ErrResponse = errors.New("invalid or ambiguous Prometheus response")

const maxSamples = 4096
const staleSeconds = 120

type sample struct {
	Metric map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}
type envelope struct {
	Status    string            `json:"status"`
	Warnings  []json.RawMessage `json:"warnings"`
	Infos     []json.RawMessage `json:"infos"`
	Partial   bool              `json:"partial"`
	IsPartial bool              `json:"isPartial"`
	Data      struct {
		ResultType string   `json:"resultType"`
		Result     []sample `json:"result"`
		Partial    bool     `json:"partial"`
		IsPartial  bool     `json:"isPartial"`
	} `json:"data"`
}

type observation struct {
	labels map[string]string
	field  string
	value  *float64
}
type source struct {
	health   SourceHealth
	stamps   map[string]*float64
	required map[string]bool
	seenUp   bool
}
type row struct {
	key     string
	labels  map[string]string
	values  map[string]*float64
	sources map[string]bool
}
type result struct {
	rows      []*row
	sources   map[string]*source
	collected time.Time
}

// FetchFleet performs two instant queries with a shared 1 MiB response budget.
func FetchFleet(ctx context.Context, access Access, filters Filters) (Fleet, error) {
	r, err := fetch(ctx, access, filters, false)
	if err != nil {
		return Fleet{}, err
	}
	out := Fleet{CollectedAt: r.collected, DataMode: "live", Rows: []Host{}, SourceHealth: allHealth(r)}
	for _, v := range r.rows {
		sources := rowHealth(v, r)
		up, age, fresh := summarize(sources)
		out.Rows = append(out.Rows, Host{Site: v.labels["site"], Rack: v.labels["rack"], Host: host(v.labels), MappingMissing: missing(v.labels, false), CPUPercent: v.values["cpu"], RAMUsedPercent: v.values["ram"], RootFilesystemUsedPercent: v.values["filesystem"], NetworkRXBytesPerSecond: v.values["rx"], NetworkTXBytesPerSecond: v.values["tx"], Up: up, SampleAgeSeconds: age, Freshness: fresh, Sources: sources})
	}
	return out, nil
}

// FetchGPU returns DCGM device observations; framebuffer gauges are MiB and
// become bytes. Device labels are context only and are not NetBox joins.
func FetchGPU(ctx context.Context, access Access, filters Filters) (GPUFleet, error) {
	r, err := fetch(ctx, access, filters, true)
	if err != nil {
		return GPUFleet{}, err
	}
	out := GPUFleet{CollectedAt: r.collected, DataMode: "live", Rows: []GPU{}, SourceHealth: allHealth(r)}
	for _, v := range r.rows {
		sources := rowHealth(v, r)
		up, age, fresh := summarize(sources)
		out.Rows = append(out.Rows, GPU{Site: v.labels["site"], Rack: v.labels["rack"], Host: host(v.labels), GPU: v.labels["gpu"], UUID: v.labels["UUID"], Model: v.labels["modelName"], MappingMissing: missing(v.labels, true), UtilizationPercent: v.values["util"], FramebufferUsedBytes: multiply(v.values["fb_used"], 1048576), FramebufferFreeBytes: multiply(v.values["fb_free"], 1048576), TemperatureCelsius: v.values["temperature"], PowerWatts: v.values["power"], Up: up, SampleAgeSeconds: age, Freshness: fresh, Sources: sources})
	}
	return out, nil
}

func fetch(ctx context.Context, access Access, filters Filters, gpu bool) (result, error) {
	for _, v := range []string{filters.Site, filters.Rack, filters.Host} {
		if !safeLabel(v) {
			return result{}, readapi.ErrConfig
		}
	}
	auth := ""
	if access.Token != "" {
		auth = "Bearer " + access.Token
	}
	c, err := readapi.New(access.URL, auth, false)
	if err != nil {
		return result{}, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	now := time.Now().UTC()
	q := queries(filters, gpu)
	values, err := query(ctx, c, q.values, now)
	if err != nil {
		return result{}, err
	}
	stamps, err := query(ctx, c, q.timestamps, now)
	if err != nil {
		return result{}, err
	}
	return assemble(values, stamps, filters, gpu, now)
}

func query(ctx context.Context, c *readapi.Client, query string, now time.Time) ([]sample, error) {
	var e envelope
	params := url.Values{"query": {query}, "timeout": {"4s"}, "limit": {"4097"}, "time": {strconv.FormatFloat(float64(now.UnixNano())/1e9, 'f', 6, 64)}}
	if err := c.GetJSON(ctx, "/api/v1/query", params, &e); err != nil {
		return nil, err
	}
	if e.Status != "success" || e.Data.ResultType != "vector" || len(e.Warnings) > 0 || len(e.Infos) > 0 || e.Partial || e.IsPartial || e.Data.Partial || e.Data.IsPartial {
		return nil, ErrResponse
	}
	if len(e.Data.Result) > maxSamples {
		return nil, readapi.ErrLimit
	}
	return e.Data.Result, nil
}

func parse(s sample, tag string) (observation, error) {
	if len(s.Value) != 2 || len(s.Metric) == 0 {
		return observation{}, ErrResponse
	}
	for k, v := range s.Metric {
		if !safeLabel(k) || !safeLabel(v) {
			return observation{}, ErrResponse
		}
	}
	var ts float64
	if json.Unmarshal(s.Value[0], &ts) != nil || math.IsNaN(ts) || math.IsInf(ts, 0) {
		return observation{}, ErrResponse
	}
	var str string
	if json.Unmarshal(s.Value[1], &str) != nil {
		return observation{}, ErrResponse
	}
	n, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return observation{}, ErrResponse
	}
	var value *float64
	if !math.IsNaN(n) && !math.IsInf(n, 0) {
		value = &n
	}
	return observation{labels: s.Metric, field: s.Metric[tag], value: value}, nil
}

func assemble(values, stamps []sample, f Filters, gpu bool, now time.Time) (result, error) {
	r := result{sources: map[string]*source{}, collected: now}
	rows := map[string]*row{}
	getSource := func(l map[string]string) *source {
		k := sourceKey(l)
		s := r.sources[k]
		if s == nil {
			s = &source{health: SourceHealth{Site: l["site"], Rack: l["rack"], Host: host(l), Job: l["job"], Instance: l["instance"], Freshness: "unknown"}, stamps: map[string]*float64{}, required: map[string]bool{"up": true}}
			r.sources[k] = s
		}
		return s
	}
	for _, v := range values {
		o, err := parse(v, "__le_field")
		if err != nil {
			return result{}, err
		}
		l := o.labels
		if !selected(l, f) {
			continue
		}
		if !knownField(o.field, gpu, false) {
			return result{}, ErrResponse
		}
		s := getSource(l)
		if o.field == "up" {
			if s.seenUp {
				return result{}, ErrResponse
			}
			s.seenUp = true
			if o.value != nil && (*o.value == 0 || *o.value == 1) {
				b := *o.value == 1
				s.health.Up = &b
			}
			if gpu {
				continue
			}
		}
		key := rowKey(l, gpu)
		rw := rows[key]
		if rw == nil {
			rw = &row{key: key, labels: l, values: map[string]*float64{}, sources: map[string]bool{}}
			rows[key] = rw
		}
		if gpu && rw.labels["modelName"] != l["modelName"] {
			return result{}, ErrResponse
		}
		rw.sources[sourceKey(l)] = true
		if o.field == "up" {
			continue
		}
		if _, exists := rw.values[o.field]; exists {
			return result{}, ErrResponse
		}
		rw.values[o.field] = o.value
		for _, required := range rawFields(o.field) {
			s.required[key+"\x00"+required] = true
		}
	}
	seen := map[string]bool{}
	for _, v := range stamps {
		o, err := parse(v, "__le_raw")
		if err != nil {
			return result{}, err
		}
		l := o.labels
		if !selected(l, f) {
			continue
		}
		if !knownField(o.field, gpu, true) {
			return result{}, ErrResponse
		}
		s := r.sources[sourceKey(l)]
		if s == nil {
			continue
		}
		key := rowKey(l, gpu) + "\x00" + sourceKey(l) + "\x00" + o.field
		if seen[key] {
			return result{}, ErrResponse
		}
		seen[key] = true
		field := rowKey(l, gpu) + "\x00" + o.field
		if o.field == "up" {
			field = "up"
		}
		s.stamps[field] = o.value
	}
	for _, s := range r.sources {
		s.health = sourceFreshness(s, s.required, now)
	}
	maxRows := 256
	if gpu {
		maxRows = 512
	}
	if len(rows) > maxRows {
		return result{}, readapi.ErrLimit
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.rows = append(r.rows, rows[k])
	}
	return r, nil
}

func safeLabel(s string) bool {
	if len(s) > 96 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == 0x2028 || r == 0x2029 {
			return false
		}
	}
	return true
}
func host(l map[string]string) string {
	for _, key := range []string{"host", "Hostname", "instance"} {
		if l[key] != "" {
			return l[key]
		}
	}
	return ""
}
func rowKey(l map[string]string, gpu bool) string {
	k := l["site"] + "\x00" + l["rack"] + "\x00" + host(l)
	if gpu {
		k += "\x00" + l["gpu"] + "\x00" + l["UUID"]
	}
	return k
}
func sourceKey(l map[string]string) string {
	return rowKey(l, false) + "\x00" + l["job"] + "\x00" + l["instance"]
}
func selected(l map[string]string, f Filters) bool {
	return (l["data_mode"] == "" || l["data_mode"] == "live") && (f.Site == "" || l["site"] == f.Site) && (f.Rack == "" || l["rack"] == f.Rack) && (f.Host == "" || host(l) == f.Host)
}
func missing(l map[string]string, gpu bool) []string {
	out := []string{}
	for _, k := range []string{"site", "rack", "host"} {
		if l[k] == "" {
			out = append(out, k)
		}
	}
	if gpu && l["gpu"] == "" && l["UUID"] == "" {
		out = append(out, "gpu")
	}
	return out
}
func rawFields(field string) []string {
	switch field {
	case "ram":
		return []string{"ram_available", "ram_total"}
	case "filesystem":
		return []string{"filesystem_available", "filesystem_size"}
	default:
		return []string{field}
	}
}
func knownField(f string, gpu, raw bool) bool {
	fields := ",cpu,ram,filesystem,rx,tx,up,"
	if raw {
		fields = ",cpu,ram_available,ram_total,filesystem_available,filesystem_size,rx,tx,up,"
	}
	if gpu {
		fields = ",util,fb_used,fb_free,temperature,power,up,"
	}
	for _, candidate := range strings.Split(fields, ",") {
		if f != "" && f == candidate {
			return true
		}
	}
	return false
}
func multiply(v *float64, by float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v * by
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return nil
	}
	return &x
}
func allHealth(r result) []SourceHealth {
	keys := make([]string, 0, len(r.sources))
	for k := range r.sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []SourceHealth{}
	for _, k := range keys {
		out = append(out, r.sources[k].health)
	}
	return out
}
func rowHealth(v *row, r result) []SourceHealth {
	keys := make([]string, 0, len(v.sources))
	for k := range v.sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []SourceHealth{}
	for _, k := range keys {
		s := r.sources[k]
		required := map[string]bool{"up": true}
		for field := range s.required {
			if strings.HasPrefix(field, v.key+"\x00") {
				required[field] = true
			}
		}
		out = append(out, sourceFreshness(s, required, r.collected))
	}
	return out
}

func sourceFreshness(s *source, required map[string]bool, now time.Time) SourceHealth {
	h := s.health
	h.Freshness = "unknown"
	h.SampleAgeSeconds = nil
	h.Healthy = false
	var oldest *float64
	for field := range required {
		stamp, ok := s.stamps[field]
		if !ok || stamp == nil || *stamp <= 0 || *stamp > float64(now.UnixNano())/1e9+5 {
			return h
		}
		if oldest == nil || *stamp < *oldest {
			oldest = stamp
		}
	}
	if oldest != nil {
		age := float64(now.UnixNano())/1e9 - *oldest
		if age < 0 {
			age = 0
		}
		h.SampleAgeSeconds = &age
		h.Freshness = "fresh"
		if age > staleSeconds {
			h.Freshness = "stale"
		}
	}
	h.Healthy = h.Up != nil && *h.Up && h.Freshness == "fresh"
	return h
}
func summarize(sources []SourceHealth) (*bool, *float64, string) {
	if len(sources) == 0 {
		return nil, nil, "unknown"
	}
	up := true
	upKnown := true
	fresh := "fresh"
	var age *float64
	for _, s := range sources {
		if s.Up == nil {
			upKnown = false
		} else if !*s.Up {
			up = false
		}
		if s.Freshness == "stale" {
			fresh = "stale"
		} else if s.Freshness != "fresh" && fresh != "stale" {
			fresh = "unknown"
		}
		if s.SampleAgeSeconds != nil && (age == nil || *s.SampleAgeSeconds > *age) {
			x := *s.SampleAgeSeconds
			age = &x
		}
	}
	var u *bool
	if !up || upKnown {
		u = &up
	}
	if fresh == "unknown" {
		age = nil
	}
	return u, age, fresh
}
