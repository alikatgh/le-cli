// Package readapi provides a deliberately bounded, read-only JSON transport.
package readapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrConfig   = errors.New("invalid read API configuration")
	ErrLimit    = errors.New("read API response limit exceeded")
	ErrResponse = errors.New("invalid read API response")
)

const bodyBudget = 1 << 20

// Client permits serialized GETs to one configured origin. Its response budget
// is shared by every request, including unsuccessful responses.
type Client struct {
	base      *url.URL
	auth      string
	http      *http.Client
	transport *http.Transport
	gate      chan struct{}
	mu        sync.Mutex
	closed    bool
	remaining int64
}

// New requires verified HTTPS, except for an explicit literal loopback address.
// rootOnly rejects a base path (for APIs whose pagination must remain at root).
func New(baseURL, authHeader string, rootOnly bool) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u == nil || u.Opaque != "" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return nil, ErrConfig
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, ErrConfig
		}
	}
	if strings.Contains(u.Hostname(), "%") || !safePath(u.Path) || (rootOnly && u.Path != "" && u.Path != "/") {
		return nil, ErrConfig
	}
	if p := u.Port(); p != "" {
		if _, err := net.LookupPort("tcp", p); err != nil {
			return nil, ErrConfig
		}
	}
	if len(authHeader) > 4096 {
		return nil, ErrConfig
	}
	for _, r := range authHeader {
		if r < 32 || r == 127 || r > 126 {
			return nil, ErrConfig
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	tr := &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, MaxConnsPerHost: 1,
		DisableKeepAlives: true, DisableCompression: true,
	}
	c := &Client{base: u, auth: authHeader, transport: tr, gate: make(chan struct{}, 1), remaining: bodyBudget}
	c.http = &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return c, nil
}

func safePath(p string) bool {
	if strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return false
		}
	}
	return true
}

// GetJSON never accepts another origin, a query embedded in the path, redirects,
// compression, or response text in returned errors.
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, dst any) error {
	if c == nil || ctx == nil {
		return ErrConfig
	}
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrConfig
	}
	if c.remaining <= 0 {
		return ErrLimit
	}
	p, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || p.IsAbs() || p.Host != "" || p.RawQuery != "" || p.ForceQuery || p.Fragment != "" || !safePath(p.Path) {
		return ErrConfig
	}
	u := *c.base
	u.Path += p.Path
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ErrConfig
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e, ok := err.(net.Error); ok && e.Timeout() {
			return context.DeadlineExceeded
		}
		return errors.New("read API request failed")
	}
	defer func() { _ = resp.Body.Close() }() // Response read errors are handled below.
	if e := resp.Header.Get("Content-Encoding"); e != "" && !strings.EqualFold(e, "identity") {
		return ErrResponse
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.remaining+1))
	c.remaining -= int64(len(data))
	if c.remaining < 0 {
		return ErrLimit
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrResponse
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("read API returned an unsuccessful status")
	}
	if !boundedDepth(data) {
		return ErrLimit
	}
	if !utf8.Valid(data) {
		return ErrResponse
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return ErrResponse
	}
	return nil
}

func boundedDepth(data []byte) bool {
	depth := 0
	quoted := false
	escaped := false
	for _, b := range data {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 128 {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}

// Close prevents subsequent requests and releases idle transport resources.
func (c *Client) Close() {
	if c != nil {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		c.transport.CloseIdleConnections()
	}
}
