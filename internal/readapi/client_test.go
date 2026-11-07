package readapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigurationRejectsUnsafeOriginsAndHeaders(t *testing.T) {
	for _, s := range []string{"http://localhost:9090", "http://example.com", "file:///tmp/test", "https://name:pass@example.com", "https://example.com?x=1", "https://example.com?", "https://example.com/#fragment", "https://example.com/a/../b", "https://example.com/%2e%2e/b", "https://:443", "https://example.com:99999"} {
		if c, err := New(s, "", false); !errors.Is(err, ErrConfig) {
			if c != nil {
				c.Close()
			}
			t.Errorf("accepted unsafe origin %q", s)
		}
	}
	for _, auth := range []string{"Bearer x\r\nX: y", strings.Repeat("a", 4097), "Bearer \x1b[31m"} {
		if _, err := New("https://example.com", auth, false); !errors.Is(err, ErrConfig) {
			t.Error("accepted unsafe header")
		}
	}
	if _, err := New("https://example.com/prefix", "", true); !errors.Is(err, ErrConfig) {
		t.Error("root-only accepted prefix")
	}
	for _, s := range []string{"https://example.com/prefix", "http://127.0.0.1:9090", "http://[::1]:9090"} {
		c, err := New(s, "", false)
		if err != nil {
			t.Errorf("rejected safe origin: %v", err)
		} else {
			c.Close()
		}
	}
}

func TestGETPrefixAuthorizationAndPathConfinement(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/prefix/api/v1/query" || r.Header.Get("Authorization") != "Bearer fixture" || r.URL.Query().Get("query") != "a + b" {
			t.Error("incorrect confined request")
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer s.Close()
	c, err := New(s.URL+"/prefix", "Bearer fixture", false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out map[string]bool
	if err := c.GetJSON(context.Background(), "/api/v1/query", url.Values{"query": {"a + b"}}, &out); err != nil || !out["ok"] {
		t.Fatalf("GET: %v, %v", out, err)
	}
	for _, p := range []string{"https://example.com", "//example.com/x", "/../x", "/%2e%2e/x", "/x?x=1", "/x#fragment"} {
		if err := c.GetJSON(context.Background(), p, nil, &out); !errors.Is(err, ErrConfig) {
			t.Errorf("accepted unsafe request path %q", p)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("unexpected requests: %d", calls.Load())
	}
	c.Close()
	if !errors.Is(c.GetJSON(context.Background(), "/x", nil, &out), ErrConfig) {
		t.Fatal("closed client allowed request")
	}
}

func TestRedirectDoesNotForwardAuthorization(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); _, _ = fmt.Fprint(w, `{}`) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	c, _ := New(s.URL, "Bearer fixture", false)
	defer c.Close()
	var out any
	if err := c.GetJSON(context.Background(), "/x", nil, &out); err == nil {
		t.Fatal("accepted redirect")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("followed cross-origin redirect")
	}
}

func TestNoEnvironmentProxyOrUnverifiedTLS(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); _, _ = fmt.Fprint(w, `{}`) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	c, _ := New("https://example.invalid", "", false)
	defer c.Close()
	if c.transport.Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	if c.transport.TLSClientConfig != nil && c.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("unverified TLS enabled")
	}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, `{}`) }))
	defer s.Close()
	tlsClient, _ := New(s.URL, "", false)
	defer tlsClient.Close()
	var out any
	if err := tlsClient.GetJSON(context.Background(), "/x", nil, &out); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
	if proxyCalls.Load() != 0 {
		t.Fatal("used environment proxy")
	}
}

func TestLifetimeBodyBudgetAndDepth(t *testing.T) {
	body := `{"padding":"` + strings.Repeat("a", 600000) + `"}`
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, body) }))
	defer s.Close()
	c, _ := New(s.URL, "", false)
	defer c.Close()
	var out any
	if err := c.GetJSON(context.Background(), "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
	if err := c.GetJSON(context.Background(), "/x", nil, &out); !errors.Is(err, ErrLimit) {
		t.Fatalf("aggregate budget: %v", err)
	}
	if err := c.GetJSON(context.Background(), "/x", nil, &out); !errors.Is(err, ErrLimit) {
		t.Fatalf("budget reset: %v", err)
	}
	body = strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129)
	d, _ := New(s.URL, "", false)
	defer d.Close()
	if err := d.GetJSON(context.Background(), "/x", nil, &out); !errors.Is(err, ErrLimit) {
		t.Fatalf("depth: %v", err)
	}
}

func TestCancellationAndSanitizedFailures(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		w.WriteHeader(500)
		_, _ = fmt.Fprint(w, "secret-server-body fixture-token")
	}))
	defer s.Close()
	c, _ := New(s.URL, "Bearer fixture-token", false)
	defer c.Close()
	var out any
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.GetJSON(ctx, "/slow", nil, &out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	err := c.GetJSON(context.Background(), "/error", nil, &out)
	if err == nil || strings.Contains(err.Error(), "fixture") || strings.Contains(err.Error(), s.URL) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe failure: %v", err)
	}
	if c.http.Timeout != 5*time.Second || c.transport.MaxResponseHeaderBytes != 32768 || c.transport.MaxConnsPerHost != 1 || !c.transport.DisableKeepAlives || !c.transport.DisableCompression {
		t.Fatal("transport bounds changed")
	}
	c.http.Timeout = 20 * time.Millisecond
	if err := c.GetJSON(context.Background(), "/slow", nil, &out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HTTP timeout: %v", err)
	}
}

func TestRejectsEncodedAndOversizedHeaders(t *testing.T) {
	for _, mode := range []string{"encoding", "headers"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "encoding" {
					w.Header().Set("Content-Encoding", "gzip")
				} else {
					w.Header().Set("X-Large", strings.Repeat("x", 40000))
				}
				_, _ = fmt.Fprint(w, `{}`)
			}))
			defer s.Close()
			c, _ := New(s.URL, "", false)
			defer c.Close()
			var out any
			if err := c.GetJSON(context.Background(), "/x", nil, &out); err == nil {
				t.Fatal("accepted unsupported response")
			}
		})
	}
}
