package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alikatgh/le-cli/internal/readapi"
)

func fixture(tag, field, value string, labels map[string]string) sample {
	l := map[string]string{"site": "west", "rack": "r1", "host": "node-a", "instance": "node-a:9100", "job": "node"}
	for k, v := range labels {
		l[k] = v
	}
	l[tag] = field
	v, _ := json.Marshal(value)
	return sample{Metric: l, Value: []json.RawMessage{json.RawMessage(`2000000000`), v}}
}
func val(field, value string, labels map[string]string) sample {
	return fixture("__le_field", field, value, labels)
}
func stamp(field string, now time.Time, age float64, labels map[string]string) sample {
	return fixture("__le_raw", field, strconv.FormatFloat(float64(now.Unix())-age, 'f', 3, 64), labels)
}
func requireNumber(t *testing.T, p *float64, want float64) {
	t.Helper()
	if p == nil || *p != want {
		t.Fatalf("number %v, want %v", p, want)
	}
}

func TestFleetHealthUsesUnderlyingSamplesAndPreservesUnknown(t *testing.T) {
	now := time.Unix(2000000000, 0)
	values := []sample{val("cpu", "27.5", nil), val("ram", "NaN", nil), val("rx", "1234", nil), val("up", "1", nil)}
	stamps := []sample{stamp("cpu", now, 500, nil), stamp("ram_available", now, 5, nil), stamp("ram_total", now, 5, nil), stamp("rx", now, 5, nil), stamp("up", now, 5, nil)}
	r, err := assemble(values, stamps, Filters{}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.rows) != 1 {
		t.Fatalf("rows: %d", len(r.rows))
	}
	requireNumber(t, r.rows[0].values["cpu"], 27.5)
	if r.rows[0].values["ram"] != nil || r.rows[0].values["tx"] != nil {
		t.Fatal("missing/nonfinite became a number")
	}
	s := allHealth(r)[0]
	if s.Freshness != "stale" || s.Healthy || s.Up == nil || !*s.Up {
		t.Fatalf("stale source falsely healthy: %+v", s)
	}
	requireNumber(t, s.SampleAgeSeconds, 500)
	// The vector evaluation timestamp is current; it must not determine age.
	for i := range stamps {
		stamps[i] = stamp(stamps[i].Metric["__le_raw"], now, 2, nil)
	}
	r, err = assemble(values, stamps, Filters{}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if !allHealth(r)[0].Healthy {
		t.Fatal("fresh up source not healthy")
	}
	r, err = assemble(values[:3], stamps, Filters{}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if allHealth(r)[0].Healthy || allHealth(r)[0].Up != nil {
		t.Fatal("missing up source falsely healthy")
	}
	r, err = assemble(values, stamps[:len(stamps)-1], Filters{}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if allHealth(r)[0].Healthy || allHealth(r)[0].Freshness != "unknown" {
		t.Fatal("missing underlying timestamp falsely healthy")
	}
}

func TestHostFallbackAndExactFilters(t *testing.T) {
	now := time.Unix(2000000000, 0)
	labels := map[string]string{"host": "", "Hostname": "dcgm-a", "instance": "dcgm-a:9400"}
	values := []sample{val("cpu", "1", labels)}
	for _, f := range []Filters{{Host: "dcgm-a"}, {Site: "west", Rack: "r1", Host: "dcgm-a"}} {
		r, err := assemble(values, nil, f, false, now)
		if err != nil || len(r.rows) != 1 {
			t.Fatalf("fallback exact filter: %v %d", err, len(r.rows))
		}
		if !reflect.DeepEqual(missing(r.rows[0].labels, false), []string{"host"}) {
			t.Fatal("fallback hid missing host mapping")
		}
	}
	for _, f := range []Filters{{Host: "dcgm"}, {Host: "dcgm-a:9400"}, {Site: "east"}, {Rack: "r"}} {
		r, err := assemble(values, nil, f, false, now)
		if err != nil || len(r.rows) != 0 {
			t.Fatal("inexact filter matched")
		}
	}
	labels["Hostname"] = ""
	r, err := assemble([]sample{val("cpu", "1", labels)}, nil, Filters{Host: "dcgm-a:9400"}, false, now)
	if err != nil || len(r.rows) != 1 {
		t.Fatal("instance fallback failed")
	}
}

func TestGPUUnitsContextAndFreshness(t *testing.T) {
	now := time.Unix(2000000000, 0)
	labels := map[string]string{"gpu": "0", "UUID": "GPU-fixture", "modelName": "Fixture GPU"}
	values := []sample{val("util", "42", labels), val("fb_used", "2", labels), val("fb_free", "4", labels), val("temperature", "61", labels), val("power", "125.5", labels), val("up", "1", nil)}
	stamps := []sample{stamp("util", now, 4, labels), stamp("fb_used", now, 4, labels), stamp("fb_free", now, 4, labels), stamp("temperature", now, 4, labels), stamp("power", now, 4, labels), stamp("up", now, 4, nil)}
	s := metricsServer(t, values, stamps)
	defer s.Close()
	g, err := FetchGPU(context.Background(), Access{URL: s.URL}, Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Rows) != 1 || g.DataMode != "live" || g.Rows[0].GPU != "0" || g.Rows[0].UUID != "GPU-fixture" || g.Rows[0].Model != "Fixture GPU" {
		t.Fatalf("GPU identity: %+v", g)
	}
	requireNumber(t, g.Rows[0].FramebufferUsedBytes, 2097152)
	requireNumber(t, g.Rows[0].FramebufferFreeBytes, 4194304)
	requireNumber(t, g.Rows[0].PowerWatts, 125.5)
	if g.Rows[0].Freshness != "fresh" || g.Rows[0].Up == nil || !*g.Rows[0].Up {
		t.Fatalf("GPU health: %+v", g.Rows[0])
	}
}

func TestGPUMissingTimestampCannotBorrowAnotherDevicesFreshness(t *testing.T) {
	now := time.Unix(2000000000, 0)
	zero := map[string]string{"gpu": "0", "UUID": "GPU-zero"}
	one := map[string]string{"gpu": "1", "UUID": "GPU-one"}
	r, err := assemble([]sample{val("util", "42", zero), val("util", "51", one), val("up", "1", nil)}, []sample{stamp("util", now, 1, zero), stamp("up", now, 1, nil)}, Filters{}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.rows) != 2 || rowHealth(r.rows[0], r)[0].Freshness != "fresh" || rowHealth(r.rows[1], r)[0].Freshness != "unknown" {
		t.Fatal("missing device timestamp borrowed another device's age")
	}
	if allHealth(r)[0].Healthy {
		t.Fatal("incomplete exporter timestamps marked healthy")
	}
}

func metricsServer(t *testing.T, values, stamps []sample) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if calls.Load() > 2 {
			t.Error("more than two requests")
		}
		if r.URL.Path != "/api/v1/query" || r.URL.Query().Get("limit") != "4097" || r.URL.Query().Get("timeout") != "4s" || r.URL.Query().Get("time") == "" {
			t.Error("missing bounded API parameters")
		}
		data := values
		if strings.Contains(r.URL.Query().Get("query"), "__le_raw") {
			data = stamps
		}
		// Use the requested instant, keeping the controlled raw age and avoiding
		// dependence on the fixture's vector evaluation timestamp.
		if data != nil && strings.Contains(r.URL.Query().Get("query"), "__le_raw") {
			instant, _ := strconv.ParseFloat(r.URL.Query().Get("time"), 64)
			copyData := make([]sample, len(data))
			copy(copyData, data)
			for i, s := range copyData {
				var old string
				_ = json.Unmarshal(s.Value[1], &old)
				n, _ := strconv.ParseFloat(old, 64)
				age := 2000000000 - n
				v, _ := json.Marshal(strconv.FormatFloat(instant-age, 'f', 6, 64))
				copyData[i].Value = []json.RawMessage{s.Value[0], v}
			}
			data = copyData
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": data}})
	}))
}

func TestRejectsAmbiguityUnsafeLabelsAndDemoContamination(t *testing.T) {
	now := time.Now()
	for _, values := range [][]sample{
		{val("cpu", "1", nil), val("cpu", "2", map[string]string{"instance": "other:9100"})},
		{val("up", "1", nil), val("up", "1", nil)},
		{val("cpu", "1", map[string]string{"host": "bad\x1b[31m"})},
		{val("cpu", "1", map[string]string{"host": "bad\u202e"})},
		{val("cpu", "1", map[string]string{"site": strings.Repeat("a", 97)})},
		{val("cpu,ram", "1", nil)},
	} {
		if _, err := assemble(values, nil, Filters{}, false, now); !errors.Is(err, ErrResponse) {
			t.Fatalf("accepted ambiguous/unsafe vector: %v", err)
		}
	}
	r, err := assemble([]sample{val("cpu", "1", map[string]string{"data_mode": "demo"}), val("cpu", "2", map[string]string{"data_mode": "snapshot"})}, nil, Filters{}, false, now)
	if err != nil || len(r.rows) != 0 {
		t.Fatal("demo/snapshot entered live output")
	}
	_, err = assemble([]sample{val("cpu", "1", nil)}, []sample{stamp("cpu", now, 1, nil), stamp("cpu", now, 1, nil)}, Filters{}, false, now)
	if !errors.Is(err, ErrResponse) {
		t.Fatal("accepted duplicate raw timestamp")
	}
}

func TestResponseLimitsPartialAndUnsupportedResults(t *testing.T) {
	for _, body := range []string{
		`{"status":"success","warnings":["truncated fixture-token"],"data":{"resultType":"vector","result":[]}}`,
		`{"status":"success","partial":true,"data":{"resultType":"vector","result":[]}}`,
		`{"status":"success","data":{"isPartial":true,"resultType":"vector","result":[]}}`,
		`{"status":"success","data":{"resultType":"matrix","result":[]}}`,
		`{"status":"error","error":"fixture-token"}`,
	} {
		t.Run(body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
			defer s.Close()
			_, err := FetchFleet(context.Background(), Access{URL: s.URL, Token: "fixture-token"}, Filters{})
			if !errors.Is(err, ErrResponse) || strings.Contains(err.Error(), "fixture-token") {
				t.Fatalf("partial/unsafe response: %v", err)
			}
		})
	}
	values := make([]sample, 4097)
	for i := range values {
		values[i] = val("cpu", "1", nil)
	}
	s := metricsServer(t, values, nil)
	defer s.Close()
	_, err := FetchFleet(context.Background(), Access{URL: s.URL}, Filters{})
	if !errors.Is(err, readapi.ErrLimit) {
		t.Fatalf("series limit: %v", err)
	}
	for _, gpu := range []bool{false, true} {
		max := 257
		if gpu {
			max = 513
		}
		v := make([]sample, max)
		for i := range v {
			labels := map[string]string{"host": fmt.Sprintf("node-%d", i), "gpu": strconv.Itoa(i)}
			field := "cpu"
			if gpu {
				field = "util"
			}
			v[i] = val(field, "1", labels)
		}
		if _, err := assemble(v, nil, Filters{}, gpu, time.Now()); !errors.Is(err, readapi.ErrLimit) {
			t.Fatalf("row limit: %v", err)
		}
	}
}

func TestQueriesQuoteExactFiltersAndUseRawTimestamp(t *testing.T) {
	f := Filters{Site: `x"} or up{job="evil`, Rack: "r\\q", Host: "node"}
	q := queries(f, false)
	if !strings.Contains(q.values, "site="+strconv.Quote(f.Site)) || !strings.Contains(q.values, "rack="+strconv.Quote(f.Rack)) {
		t.Fatal("filter was not quoted")
	}
	if !strings.Contains(q.timestamps, "timestamp(node_cpu_seconds_total") || strings.Contains(q.timestamps, "timestamp(rate(") {
		t.Fatal("freshness uses evaluated value")
	}
	if strings.Contains(q.values, "le_dc_demo") || !strings.Contains(q.values, `data_mode=~"live|"`) {
		t.Fatal("production query includes demo fallback")
	}
	if _, err := FetchFleet(context.Background(), Access{URL: "http://127.0.0.1"}, Filters{Host: "bad\x1b"}); !errors.Is(err, readapi.ErrConfig) {
		t.Fatal("unsafe filter accepted")
	}
}
