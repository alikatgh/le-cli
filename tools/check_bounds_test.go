package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func boundedCheckServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	t.Cleanup(server.Close)
	return server
}

// Only the fixture's reserved address can be contacted. The other loopback
// family fails in memory, even if an unrelated process uses the same port.
func checkFixture(ctx context.Context, server *httptest.Server, out io.Writer) error {
	expected := server.Listener.Addr().String()
	dialer := net.Dialer{Timeout: healthProbeTimeout}
	return checkContext(ctx, checkTestPort(server), out, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != expected {
			return nil, errors.New("unconfigured fixture address")
		}
		return dialer.DialContext(ctx, network, address)
	})
}

func checkTestPort(server *httptest.Server) string {
	return fmt.Sprint(server.Listener.Addr().(*net.TCPAddr).Port)
}

func TestCheckDoesNotFollowRedirectsOrProxies(t *testing.T) {
	var contacted atomic.Int32
	canary := boundedCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		contacted.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	t.Setenv("HTTP_PROXY", canary.URL)
	t.Setenv("HTTPS_PROXY", canary.URL)
	t.Setenv("ALL_PROXY", canary.URL)
	server := boundedCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("health checks must not forward credentials")
		}
		w.Header().Set("Location", canary.URL)
		w.WriteHeader(http.StatusFound)
	})
	var out bytes.Buffer
	if err := checkFixture(context.Background(), server, &out); err != nil {
		t.Fatal(err)
	}
	if contacted.Load() != 0 || !strings.Contains(out.String(), "302 Found") {
		t.Fatalf("redirect/proxy escaped loopback check: calls=%d, output=%q", contacted.Load(), out.String())
	}
}

func TestCheckHeaderAndInterimLimits(t *testing.T) {
	for _, test := range []struct {
		name      string
		header    int
		interim   int
		wantError bool
	}{
		{"large final", healthMaxHeaderBytes, 0, true},
		{"eight interim allowed", 0, 8, false},
		{"ninth interim refused", 0, 9, true},
		{"aggregate interim headers", 5000, 8, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := boundedCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
				if test.header > 0 {
					w.Header().Set("X-Bounded", strings.Repeat("a", test.header))
				}
				for range test.interim {
					w.WriteHeader(http.StatusEarlyHints)
				}
				w.WriteHeader(http.StatusOK)
			})
			var out bytes.Buffer
			err := checkFixture(context.Background(), server, &out)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v, want failure=%v", err, test.wantError)
			}
			if err != nil && out.Len() != 0 {
				t.Fatal("failed check wrote partial output")
			}
		})
	}
}

func TestCheckReturnsAfterHeadersWithoutWaitingForBody(t *testing.T) {
	server := boundedCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	var out bytes.Buffer
	started := time.Now()
	if err := checkFixture(context.Background(), server, &out); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("health check waited for the unending body")
	}
}

func TestCheckCancellationClosesConnection(t *testing.T) {
	entered := make(chan struct{})
	server := boundedCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var out bytes.Buffer
	go func() { done <- checkFixture(ctx, server, &out) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach fixture")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || out.Len() != 0 {
			t.Fatal("cancelled request must fail without output")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request kept the connection open")
	}
}

func TestCheckRefusesUpgradeAndBadPorts(t *testing.T) {
	server := boundedCheckServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	for _, port := range []string{checkTestPort(server), "", "0", "+1", "-1", "65536", "80/path", " 80", "８０"} {
		var out bytes.Buffer
		if err := checkContext(context.Background(), port, &out, func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != server.Listener.Addr().String() {
				return nil, errors.New("unconfigured fixture address")
			}
			return (&net.Dialer{Timeout: healthProbeTimeout}).DialContext(ctx, network, address)
		}); err == nil || out.Len() != 0 {
			t.Errorf("port %q should fail without output", port)
		}
	}
}

func TestCheckIPv6Loopback(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback unavailable")
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	server.Listener = listener
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	defer server.Close()
	var out bytes.Buffer
	if err := checkFixture(context.Background(), server, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "204 No Content") {
		t.Fatal("IPv6 response missing")
	}
}
