package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckReachesAListener(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// srv.URL is like http://127.0.0.1:PORT — Check hits 127.0.0.1:PORT itself.
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	if err := Check(port); err != nil {
		t.Errorf("Check should reach the local test server, got %v", err)
	}
}

func TestCheckDetectsHTTPS(t *testing.T) {
	// A real TLS server with a self-signed cert — the vite --https / caddy
	// shape. Check must detect the scheme via the TLS probe and still reach
	// the HTTP status (cert verification skipped for the localhost check).
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	if err := Check(port); err != nil {
		t.Errorf("Check should reach the local https test server, got %v", err)
	}
}

func TestCheckErrorsOnClosedPort(t *testing.T) {
	// Nothing listens on :1 → connection refused → a bounded error, not a hang.
	if err := Check("1"); err == nil {
		t.Error("Check on a closed port should return an error")
	}
}

func TestCheckRejectsBadPort(t *testing.T) {
	if err := Check("notaport"); err == nil {
		t.Error("Check should reject an invalid port before dialing")
	}
}
