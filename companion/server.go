// Package companion provides the explicitly started, optional local macOS API.
// It exposes typed operations on server-owned observations, never a command API.
package companion

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/internal/boundedexec"
	"github.com/alikatgh/le-cli/kill"
	"github.com/alikatgh/le-cli/scan"
)

const (
	MaxBody           = 16 << 10
	MaxHeaders        = 8 << 10
	MaxResponse       = 4 << 20
	MaxRows           = 4096
	SnapshotTTL       = 120 * time.Second
	ReceiptTTL        = 10 * time.Minute
	MaxReceipts       = 128
	MaxCPU            = 1_000_000.0
	MaxRSSKiB   int64 = math.MaxInt64 / 1024
)

type Row struct {
	TargetID string `json:"targetID"`
	scan.Listener
	// Shadow legacy fields: zero does not prove a successful measurement.
	CPU     *float64      `json:"cpu,omitempty"`
	RSS     *int          `json:"rss,omitempty"`
	Profile intel.Profile `json:"profile"`
}

// Engine implementations must honor cancellation through all child work.
type Engine interface {
	Snapshot(context.Context) ([]Row, error)
	Action(context.Context, Row, string) (string, error)
}

type LocalEngine struct{}

func (LocalEngine) Snapshot(ctx context.Context) ([]Row, error) {
	listeners, err := scan.ScanContext(ctx)
	if err != nil {
		return nil, err
	}
	if len(listeners) > MaxRows {
		return nil, errors.New("listener row limit exceeded")
	}
	env, err := intel.DetectContext(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(listeners))
	for _, l := range listeners {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows = append(rows, Row{Listener: l, Profile: intel.Make(l, env)})
	}
	return rows, nil
}

func (LocalEngine) Action(ctx context.Context, row Row, operation string) (string, error) {
	switch operation {
	case "stop":
		return kill.StopContext(ctx, row.Listener, row.Profile)
	case "restartManaged":
		return kill.RestartContext(ctx, row.Listener, row.Profile)
	default:
		return "", kill.ErrRefused
	}
}

type Status struct {
	ProtocolVersion int      `json:"protocolVersion"`
	ServerInstance  string   `json:"serverInstance"`
	CLIVersion      string   `json:"cliVersion"`
	Capabilities    []string `json:"capabilities"`
}

type Snapshot struct {
	SnapshotID string    `json:"snapshotID"`
	CapturedAt time.Time `json:"capturedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Rows       []Row     `json:"rows"`
}

type ActionRequest struct {
	RequestID  string `json:"requestID"`
	SnapshotID string `json:"snapshotID"`
	TargetID   string `json:"targetID"`
	Operation  string `json:"operation"`
}

type Receipt struct {
	ActionRequest
	State   string `json:"state"`
	Outcome string `json:"outcome,omitempty"`
	Message string `json:"message"`
}

type cachedReceipt struct {
	receipt Receipt
	expires time.Time
}

type Server struct {
	host, token, instance, version string
	engine                         Engine
	work                           chan struct{}
	workMu                         sync.Mutex
	closing                        bool
	mu                             sync.Mutex
	snapshot                       *Snapshot
	snapshotTimer                  *time.Timer
	receipts                       map[string]cachedReceipt
	now                            func() time.Time
	snapshotTimeout, actionTimeout time.Duration
}

// NewHandler binds request checks to the exact listener host and ephemeral
// credential. It does not open a socket or persist the credential.
func NewHandler(host, token, instance, version string, engine Engine) *Server {
	return &Server{host: host, token: token, instance: instance, version: version,
		engine: engine, work: make(chan struct{}, 1), receipts: make(map[string]cachedReceipt),
		now: time.Now, snapshotTimeout: 10 * time.Second, actionTimeout: 12 * time.Second}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !s.allowedRequest(r) {
		writeError(w, 403, "requestRejected", "Request source is not permitted.")
		return
	}
	want := "Bearer " + s.token
	got := r.Header.Get("Authorization")
	if len(s.token) == 0 || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		writeError(w, 401, "unauthorized", "A valid connection credential is required.")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method", "Use POST.")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, 415, "mediaType", "Use application/json.")
		return
	}
	if r.ContentLength > MaxBody {
		writeError(w, 413, "bodyLimit", "Request body is too large.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	switch r.URL.Path {
	case "/v1/status":
		if !decodeBody(w, r, &struct{}{}) {
			return
		}
		writeJSON(w, 200, Status{1, s.instance, s.version, []string{"inspect", "stop", "restartManaged"}})
	case "/v1/snapshot":
		if !decodeBody(w, r, &struct{}{}) {
			return
		}
		s.serveSnapshot(w, r)
	case "/v1/action":
		var request ActionRequest
		if !decodeBody(w, r, &request) {
			return
		}
		s.serveAction(w, r, request)
	default:
		writeError(w, 404, "route", "Unknown route.")
	}
}

func (s *Server) allowedRequest(r *http.Request) bool {
	if r.Host != s.host || r.URL.RawQuery != "" || r.URL.Fragment != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return false
	}
	size := len(r.Method) + len(r.RequestURI) + len(r.Proto) + len(r.Host) + 16
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		if lower == "origin" || strings.HasPrefix(lower, "sec-fetch-") {
			return false
		}
		for _, value := range values {
			size += len(name) + len(value) + 4
		}
	}
	return size <= MaxHeaders
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	data, err := io.ReadAll(r.Body)
	if err == nil {
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) < 2 || trimmed[0] != '{' {
			err = errors.New("object required")
		} else {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			err = decoder.Decode(value)
			if err == nil {
				var extra any
				if decoder.Decode(&extra) != io.EOF {
					err = errors.New("extra JSON")
				}
			}
		}
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, 413, "bodyLimit", "Request body is too large.")
		} else {
			writeError(w, 400, "invalidJSON", "Request JSON is invalid.")
		}
		return false
	}
	return true
}

func (s *Server) claimWork() bool {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	if s.closing {
		return false
	}
	select {
	case s.work <- struct{}{}:
		return true
	default:
		return false
	}
}

// stopAndWait prevents new engine work and waits for the bounded active
// operation to retire all owned child processes before the CLI may exit.
func (s *Server) stopAndWait() {
	s.workMu.Lock()
	s.closing = true
	s.workMu.Unlock()
	s.work <- struct{}{}
	<-s.work
	s.mu.Lock()
	s.clearSnapshotLocked()
	s.mu.Unlock()
}

func (s *Server) clearSnapshotLocked() {
	s.snapshot = nil
	if s.snapshotTimer != nil {
		s.snapshotTimer.Stop()
		s.snapshotTimer = nil
	}
}

func (s *Server) serveSnapshot(w http.ResponseWriter, r *http.Request) {
	if !s.claimWork() {
		writeError(w, 429, "busy", "Refresh after the current operation completes.")
		return
	}
	defer func() { <-s.work }()
	s.mu.Lock()
	s.clearSnapshotLocked()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), s.snapshotTimeout)
	defer cancel()
	rows, err := s.engine.Snapshot(ctx)
	if ctx.Err() != nil {
		writeError(w, 504, "deadline", "Snapshot deadline elapsed; refresh.")
		return
	}
	if err != nil {
		writeError(w, 500, "snapshotFailed", "A complete snapshot could not be collected.")
		return
	}
	if len(rows) > MaxRows {
		writeError(w, 500, "rowLimit", "Snapshot exceeds the row limit.")
		return
	}
	if rows == nil {
		rows = []Row{}
	}
	for i := range rows {
		rows[i].CPU, rows[i].RSS = nil, nil
		if cpu := rows[i].Listener.CPU; cpu > 0 && cpu <= MaxCPU && !math.IsInf(cpu, 0) && !math.IsNaN(cpu) {
			rows[i].CPU = &cpu
		}
		if rss := rows[i].Listener.RSS; rss > 0 && int64(rss) <= MaxRSSKiB {
			rows[i].RSS = &rss
		}
		rows[i].TargetID, err = randomID()
		if err != nil {
			writeError(w, 500, "randomness", "Unable to create a snapshot.")
			return
		}
	}
	id, err := randomID()
	if err != nil {
		writeError(w, 500, "randomness", "Unable to create a snapshot.")
		return
	}
	now := s.now().UTC()
	snapshot := &Snapshot{id, now, now.Add(SnapshotTTL), rows}
	encoded, err := encodeLimited(snapshot)
	if err != nil {
		writeError(w, 500, "responseLimit", "Snapshot exceeds the response limit.")
		return
	}
	if ctx.Err() != nil {
		writeError(w, 504, "deadline", "Snapshot deadline elapsed; refresh.")
		return
	}
	s.mu.Lock()
	s.snapshot = snapshot
	s.snapshotTimer = time.AfterFunc(SnapshotTTL, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.snapshot == snapshot {
			s.snapshot = nil
			s.snapshotTimer = nil
		}
	})
	s.mu.Unlock()
	w.WriteHeader(200)
	_, _ = w.Write(encoded)
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s *Server) serveAction(w http.ResponseWriter, r *http.Request, request ActionRequest) {
	if !idPattern.MatchString(request.RequestID) || !idPattern.MatchString(request.SnapshotID) || !idPattern.MatchString(request.TargetID) ||
		(request.Operation != "stop" && request.Operation != "restartManaged") {
		writeError(w, 400, "invalidAction", "Action IDs or operation are invalid.")
		return
	}
	s.mu.Lock()
	for id, cached := range s.receipts {
		if cached.receipt.State == "final" && !s.now().Before(cached.expires) {
			delete(s.receipts, id)
		}
	}
	if cached, ok := s.receipts[request.RequestID]; ok {
		s.mu.Unlock()
		if cached.receipt.ActionRequest != request {
			writeError(w, 409, "requestConflict", "Request ID already belongs to another action.")
			return
		}
		status := 200
		if cached.receipt.State == "pending" {
			status = 202
		}
		writeJSON(w, status, cached.receipt)
		return
	}
	if len(s.receipts) >= MaxReceipts {
		s.mu.Unlock()
		writeError(w, 429, "receiptLimit", "Action receipt capacity reached; wait before retrying.")
		return
	}
	if !s.claimWork() {
		s.mu.Unlock()
		writeError(w, 429, "busy", "Wait for the current operation to complete.")
		return
	}
	defer func() { <-s.work }()
	var row Row
	found := false
	if s.snapshot != nil && s.snapshot.SnapshotID == request.SnapshotID && s.now().Before(s.snapshot.ExpiresAt) {
		for _, candidate := range s.snapshot.Rows {
			if candidate.TargetID == request.TargetID {
				row = candidate
				found = true
				break
			}
		}
	}
	if !found {
		s.mu.Unlock()
		writeError(w, 409, "staleSnapshot", "Target is unavailable or expired; refresh.")
		return
	}
	s.clearSnapshotLocked()
	receipt := Receipt{ActionRequest: request, State: "pending", Message: "Operation is running."}
	s.receipts[request.RequestID] = cachedReceipt{receipt, s.now().Add(ReceiptTTL)}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), s.actionTimeout)
	defer cancel()
	message, err := s.engine.Action(ctx, row, request.Operation)
	receipt.State = "final"
	switch {
	case ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, boundedexec.ErrOutputLimit):
		receipt.Outcome = "unknown"
		receipt.Message = "Operation outcome is unknown; refresh before any further action."
	case errors.Is(err, kill.ErrRefused):
		receipt.Outcome = "refused"
		receipt.Message = "Identity or action safety could not be verified; refresh."
	case err != nil:
		receipt.Outcome = "failed"
		receipt.Message = "Operation failed; refresh to inspect current state."
	default:
		receipt.Outcome = "completed"
		receipt.Message = boundedMessage(message)
	}
	s.mu.Lock()
	s.receipts[request.RequestID] = cachedReceipt{receipt, s.now().Add(ReceiptTTL)}
	s.mu.Unlock()
	writeJSON(w, 200, receipt)
}

func boundedMessage(s string) string {
	if len(s) > 512 {
		return "Operation returned successfully; refresh to inspect current state."
	}
	return s
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

var errResponseLimit = errors.New("response limit exceeded")

type responseBuffer struct{ bytes.Buffer }

func (b *responseBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxResponse-b.Len() {
		return 0, errResponseLimit
	}
	return b.Buffer.Write(p)
}
func encodeLimited(value any) ([]byte, error) {
	var b responseBuffer
	err := json.NewEncoder(&b).Encode(value)
	return b.Bytes(), err
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	b, err := encodeLimited(value)
	if err != nil {
		writeError(w, 500, "responseLimit", "Response exceeds the limit.")
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message}})
}
