package companion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/kill"
	"github.com/alikatgh/le-cli/scan"
)

type fakeEngine struct {
	snap                   func(context.Context) ([]Row, error)
	act                    func(context.Context, Row, string) (string, error)
	snapCalls, actionCalls atomic.Int32
}

func (f *fakeEngine) Snapshot(ctx context.Context) ([]Row, error) {
	f.snapCalls.Add(1)
	if f.snap != nil {
		return f.snap(ctx)
	}
	return []Row{{Listener: scan.Listener{PID: 123, StartTime: "stable", CommandLineFallback: true}, Profile: intel.Profile{StopKind: intel.StopTerm}}}, nil
}
func (f *fakeEngine) Action(ctx context.Context, row Row, operation string) (string, error) {
	f.actionCalls.Add(1)
	if f.act != nil {
		return f.act(ctx, row, operation)
	}
	return "sent SIGTERM (exit unconfirmed)", nil
}

const testHost = "127.0.0.1:43210"
const testToken = "nonsecret-test-token"

func newTestServer(f *fakeEngine) *Server {
	return NewHandler(testHost, testToken, strings.Repeat("a", 32), "test", f)
}
func request(s *Server, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://"+testHost+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func snapshot(t *testing.T, s *Server) Snapshot {
	t.Helper()
	w := request(s, "/v1/snapshot", "{}")
	if w.Code != 200 {
		t.Fatalf("snapshot status %d: %s", w.Code, w.Body)
	}
	var value Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func actionJSON(s Snapshot, requestID string) string {
	b, _ := json.Marshal(ActionRequest{requestID, s.SnapshotID, s.Rows[0].TargetID, "stop"})
	return string(b)
}

func TestRejectedRequestsNeverCallEngine(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*http.Request)
		body   string
		status int
	}{
		{"missing auth", func(r *http.Request) { r.Header.Del("Authorization") }, "{}", 401},
		{"wrong auth", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, "{}", 401},
		{"origin", func(r *http.Request) { r.Header["Origin"] = []string{""} }, "{}", 403},
		{"fetch", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "none") }, "{}", 403},
		{"host", func(r *http.Request) { r.Host = "localhost:43210" }, "{}", 403},
		{"peer", func(r *http.Request) { r.RemoteAddr = "192.168.1.2:12" }, "{}", 403},
		{"method", func(r *http.Request) { r.Method = "GET" }, "{}", 405},
		{"media", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, "{}", 415},
		{"header limit", func(r *http.Request) { r.Header.Set("X-Long", strings.Repeat("x", MaxHeaders)) }, "{}", 403},
		{"unknown field", func(r *http.Request) {}, `{"pid":123}`, 400},
		{"null", func(r *http.Request) {}, "null", 400},
		{"multiple values", func(r *http.Request) {}, "{} {}", 400},
		{"oversize", func(r *http.Request) {}, strings.Repeat(" ", MaxBody+1), 413},
		{"chunked oversize", func(r *http.Request) { r.ContentLength = -1 }, strings.Repeat(" ", MaxBody+1), 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeEngine{}
			s := newTestServer(f)
			r := httptest.NewRequest("POST", "http://"+testHost+"/v1/snapshot", strings.NewReader(tc.body))
			r.RemoteAddr = "127.0.0.1:1"
			r.Header.Set("Authorization", "Bearer "+testToken)
			r.Header.Set("Content-Type", "application/json")
			tc.mutate(r)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d want %d", w.Code, tc.status)
			}
			if f.snapCalls.Load() != 0 || f.actionCalls.Load() != 0 {
				t.Fatal("rejected request reached engine")
			}
		})
	}
}

func TestSnapshotContractAndBounds(t *testing.T) {
	f := &fakeEngine{}
	s := newTestServer(f)
	first := snapshot(t, s)
	if len(first.Rows) != 1 || !idPattern.MatchString(first.SnapshotID) || !idPattern.MatchString(first.Rows[0].TargetID) || first.ExpiresAt.Sub(first.CapturedAt) != SnapshotTTL {
		t.Fatal("snapshot contract mismatch")
	}
	if !s.snapshot.Rows[0].CommandLineFallback {
		t.Fatal("internal fallback provenance lost")
	}
	second := snapshot(t, s)
	if first.SnapshotID == second.SnapshotID {
		t.Fatal("snapshot IDs reused")
	}
	if w := request(s, "/v1/action", actionJSON(first, strings.Repeat("1", 32))); w.Code != 409 {
		t.Fatalf("old snapshot accepted: %d", w.Code)
	}
	for _, tc := range []struct {
		name string
		rows []Row
	}{{"rows", make([]Row, MaxRows+1)}, {"response", []Row{{Listener: scan.Listener{CommandLine: strings.Repeat("x", MaxResponse)}}}}} {
		t.Run(tc.name, func(t *testing.T) {
			f.snap = func(context.Context) ([]Row, error) { return tc.rows, nil }
			if w := request(s, "/v1/snapshot", "{}"); w.Code != 500 {
				t.Fatal(w.Code)
			}
			if s.snapshot != nil {
				t.Fatal("partial or oversized snapshot retained")
			}
		})
	}
	f.snap = func(context.Context) ([]Row, error) { return nil, nil }
	empty := snapshot(t, s)
	if empty.Rows == nil {
		t.Fatal("rows serialized null")
	}
}

func TestResourcesOmitUnavailableAndZeroMeasurements(t *testing.T) {
	cases := []struct {
		cpu              float64
		rss              int
		wantCPU, wantRSS bool
	}{{0, 0, false, false}, {math.NaN(), -1, false, false}, {math.Inf(1), 0, false, false}, {12.5, 1024, true, true}, {MaxCPU, 1024, true, true}, {MaxCPU + 1, 1024, false, true}}
	if strconv.IntSize == 64 {
		maxRSS := MaxRSSKiB
		cases = append(cases, struct {
			cpu              float64
			rss              int
			wantCPU, wantRSS bool
		}{12.5, int(maxRSS), true, true}, struct {
			cpu              float64
			rss              int
			wantCPU, wantRSS bool
		}{12.5, int(maxRSS + 1), true, false})
	}
	for _, tc := range cases {
		f := &fakeEngine{snap: func(context.Context) ([]Row, error) {
			return []Row{{Listener: scan.Listener{CPU: tc.cpu, RSS: tc.rss}}}, nil
		}}
		w := request(newTestServer(f), "/v1/snapshot", "{}")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var wire struct {
			Rows []map[string]json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		_, cpuPresent := wire.Rows[0]["cpu"]
		_, rssPresent := wire.Rows[0]["rss"]
		if cpuPresent != tc.wantCPU || rssPresent != tc.wantRSS {
			t.Fatalf("resource presence cpu=%v rss=%v for %+v", cpuPresent, rssPresent, tc)
		}
	}
}

func TestActionReplayAndSnapshotConsumption(t *testing.T) {
	f := &fakeEngine{}
	s := newTestServer(f)
	snap := snapshot(t, s)
	body := actionJSON(snap, strings.Repeat("1", 32))
	w := request(s, "/v1/action", body)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var receipt Receipt
	_ = json.Unmarshal(w.Body.Bytes(), &receipt)
	if receipt.Outcome != "completed" || receipt.State != "final" {
		t.Fatal(w.Body.String())
	}
	if w2 := request(s, "/v1/action", body); w2.Body.String() != w.Body.String() {
		t.Fatal("replay changed receipt")
	}
	if f.actionCalls.Load() != 1 {
		t.Fatal("duplicate reran action")
	}
	if w := request(s, "/v1/action", strings.Replace(body, `"stop"`, `"restartManaged"`, 1)); w.Code != 409 {
		t.Fatal("conflicting ID accepted")
	}
	if w := request(s, "/v1/action", actionJSON(snap, strings.Repeat("2", 32))); w.Code != 409 {
		t.Fatal("consumed snapshot accepted")
	}
}

func TestPendingReplayBusyAndDeadlineDiscard(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	f := &fakeEngine{act: func(ctx context.Context, row Row, operation string) (string, error) {
		close(started)
		<-release
		return "late success", nil
	}}
	s := newTestServer(f)
	s.actionTimeout = 10 * time.Millisecond
	snap := snapshot(t, s)
	body := actionJSON(snap, strings.Repeat("1", 32))
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(s, "/v1/action", body) }()
	<-started
	if w := request(s, "/v1/action", body); w.Code != 202 {
		t.Fatal("pending replay", w.Code)
	}
	if w := request(s, "/v1/snapshot", "{}"); w.Code != 429 {
		t.Fatal("work queued", w.Code)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	w := <-done
	if !strings.Contains(w.Body.String(), `"outcome":"unknown"`) {
		t.Fatal("late action success retained", w.Body.String())
	}
	if f.actionCalls.Load() != 1 {
		t.Fatal("pending duplicate reran")
	}
}

func TestSnapshotDeadlineDiscardsLateResult(t *testing.T) {
	f := &fakeEngine{snap: func(ctx context.Context) ([]Row, error) { <-ctx.Done(); return []Row{{}}, nil }}
	s := newTestServer(f)
	s.snapshotTimeout = time.Millisecond
	if w := request(s, "/v1/snapshot", "{}"); w.Code != 504 {
		t.Fatal(w.Code)
	}
	if s.snapshot != nil {
		t.Fatal("late result published")
	}
}

func TestForgedExpiredAndUnsafeActionInput(t *testing.T) {
	f := &fakeEngine{}
	s := newTestServer(f)
	snap := snapshot(t, s)
	valid := actionJSON(snap, strings.Repeat("1", 32))
	for _, body := range []string{strings.Replace(valid, `"stop"`, `"shell"`, 1), strings.Replace(valid, `"requestID":"`+strings.Repeat("1", 32)+`"`, `"requestID":"1"`, 1), strings.TrimSuffix(valid, "}") + `,"profile":{"stopKind":"term"}}`} {
		if w := request(s, "/v1/action", body); w.Code != 400 {
			t.Fatal(w.Code, body)
		}
	}
	forged := strings.Replace(valid, snap.Rows[0].TargetID, strings.Repeat("f", 32), 1)
	if w := request(s, "/v1/action", forged); w.Code != 409 {
		t.Fatal(w.Code)
	}
	s.now = func() time.Time { return snap.ExpiresAt }
	if w := request(s, "/v1/action", valid); w.Code != 409 {
		t.Fatal("expired action accepted")
	}
	if f.actionCalls.Load() != 0 {
		t.Fatal("forged input reached action")
	}
}

func TestReceiptCapacityAndOutcomes(t *testing.T) {
	for _, tc := range []struct {
		err     error
		outcome string
	}{{kill.ErrRefused, "refused"}, {errors.New("do not leak command output"), "failed"}, {context.DeadlineExceeded, "unknown"}} {
		f := &fakeEngine{act: func(context.Context, Row, string) (string, error) { return "", tc.err }}
		s := newTestServer(f)
		snap := snapshot(t, s)
		w := request(s, "/v1/action", actionJSON(snap, strings.Repeat("1", 32)))
		if !bytes.Contains(w.Body.Bytes(), []byte(`"outcome":"`+tc.outcome+`"`)) || strings.Contains(w.Body.String(), "do not leak") {
			t.Fatal(w.Body.String())
		}
	}
	f := &fakeEngine{}
	s := newTestServer(f)
	snap := snapshot(t, s)
	for i := 0; i < MaxReceipts; i++ {
		s.receipts[string(rune(i))] = cachedReceipt{Receipt{State: "final"}, time.Now().Add(time.Hour)}
	}
	if w := request(s, "/v1/action", actionJSON(snap, strings.Repeat("1", 32))); w.Code != 429 {
		t.Fatal(w.Code)
	}
	if f.actionCalls.Load() != 0 {
		t.Fatal("capacity overflow executed")
	}
}
