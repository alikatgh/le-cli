package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alikatgh/le-cli/internal/readapi"
)

func sample(id int64) record {
	r := record{ID: id, Name: fmt.Sprintf("demo-%d", id), Site: &association{ID: 1, Name: "demo-east"}, Rack: &association{ID: 2, Name: "demo-rack"}, Tenant: &association{ID: 3, Name: "demo-owner"}, Role: &association{ID: 4, Name: "compute"}}
	r.Status.Value = "active"
	return r
}
func serve(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}
func TestPaginationUsesOnlyOwnedOriginAndV2Auth(t *testing.T) {
	var calls atomic.Int32
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/dcim/devices/" || r.URL.Query().Get("limit") != "64" || r.Header.Get("Authorization") != "Bearer nbt_unit_test" {
			t.Error("wrong read-only request")
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		rows := []record{}
		for i := offset; i < offset+64 && i < 65; i++ {
			rows = append(rows, sample(int64(i+1)))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 65, "results": rows, "next": "https://unrequested.example.invalid/private"})
	})
	// #nosec G101 -- Synthetic token for this test's owned loopback fixture, never an account credential.
	result, err := Fetch(context.Background(), Access{URL: s.URL, Token: "nbt_unit_test"}, Filters{}, false)
	if err != nil || result.Devices != 65 || result.SourceDevices != 65 || len(result.Rows) != 0 || calls.Load() != 2 {
		t.Fatalf("snapshot=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}
func TestAggregatePrivacyAndExplicitContext(t *testing.T) {
	rows := []record{sample(1), sample(2)}
	rows[1].Tenant = nil
	rows[1].Rack = nil
	rows[1].Status.Value = "future-custom"
	aggregate := summarize(rows, Filters{}, false, "live", time.Now())
	data, _ := json.Marshal(aggregate)
	if strings.Contains(string(data), "demo-owner") || strings.Contains(string(data), "demo-1") || aggregate.Unassigned["owner"] != 1 || aggregate.Lifecycle["unknown"] != 1 {
		t.Fatal("aggregate exported details or omitted missing context")
	}
	detailed := summarize(rows, Filters{Host: "demo-1"}, true, "live", time.Now())
	if detailed.Devices != 1 || len(detailed.Rows) != 1 || detailed.Rows[0].Owner != "demo-owner" || detailed.SourceDevices != 2 {
		t.Fatalf("unexpected filtered context: %+v", detailed)
	}
}
func TestMalformedChangingOrOversizeCohortFailsWhole(t *testing.T) {
	for _, scenario := range []string{"duplicate", "missing-count", "oversize", "partial", "changing", "unsafe-label"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			s := serve(t, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				rows := []record{sample(1)}
				count := 1
				switch scenario {
				case "duplicate":
					count = 2
					rows = append(rows, sample(1))
				case "missing-count":
					_, _ = w.Write([]byte(`{"results":[]}`))
					return
				case "oversize":
					count = 513
				case "partial":
					count = 2
				case "changing":
					count = 65
					if n == 1 {
						for i := 2; i <= 64; i++ {
							rows = append(rows, sample(int64(i)))
						}
					} else {
						count = 64
					}
				case "unsafe-label":
					rows[0].Name = "bad\x1b[31m"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"count": count, "results": rows})
			})
			got, err := Fetch(context.Background(), Access{URL: s.URL, Token: "unit-test"}, Filters{}, true)
			if err == nil || got.Devices != 0 || len(got.Rows) != 0 {
				t.Fatalf("partial inventory published: %+v %v", got, err)
			}
		})
	}
}
func TestCancellationAndErrorsDoNotExposeUpstreamData(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") == "0" {
			<-r.Context().Done()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := Fetch(ctx, Access{URL: s.URL, Token: "unit-test"}, Filters{}, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded cancellation, got %v", err)
	}
	failure := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("unit-test-sensitive-body"))
	})
	if _, err := Fetch(context.Background(), Access{URL: failure.URL, Token: "unit-test"}, Filters{}, false); err == nil || strings.Contains(err.Error(), "unit-test-sensitive") {
		t.Fatal("unsanitized upstream failure")
	}
}
func TestConfigAndOfflineDemo(t *testing.T) {
	if _, err := Fetch(context.Background(), Access{URL: "https://example.invalid"}, Filters{}, false); !errors.Is(err, readapi.ErrConfig) {
		t.Fatal("missing token must fail before network")
	}
	if _, err := Fetch(context.Background(), Access{URL: "https://example.invalid", Token: "unit-test"}, Filters{Host: "bad\nrow"}, false); !errors.Is(err, readapi.ErrConfig) {
		t.Fatal("unsafe filter must fail before network")
	}
	got, err := Demo(Filters{Site: "demo-east"}, true)
	if err != nil || got.DataMode != "demo" || got.Devices != 2 || len(got.Rows) != 2 {
		t.Fatal("invalid labelled offline demo")
	}
}
