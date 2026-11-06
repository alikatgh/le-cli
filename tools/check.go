package tools

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

const (
	healthTimeout             = 5 * time.Second
	healthProbeTimeout        = 350 * time.Millisecond
	healthMaxHeaderBytes      = 32 * 1024
	healthMaxInterimResponses = 8
)

// Check reports one loopback response, without redirects or response-body reads.
func Check(port string) error {
	return CheckContext(context.Background(), port, os.Stdout)
}

// CheckContext bounds TLS detection and HTTP together. It never uses DNS,
// environment proxies, cookies or the default HTTP transport.
type healthDialFunc func(context.Context, string, string) (net.Conn, error)

func CheckContext(parent context.Context, port string, out io.Writer) error {
	dialer := net.Dialer{Timeout: healthProbeTimeout}
	return checkContext(parent, port, out, dialer.DialContext)
}

func checkContext(parent context.Context, port string, out io.Writer, dial healthDialFunc) error {
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("%w: port must contain only decimal digits", ErrInvalidPort)
		}
	}
	if err := validPort(port); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, healthTimeout)
	defer cancel()
	started := time.Now()
	for _, host := range []string{"127.0.0.1", "::1"} {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("local HTTP check: %w", err)
		}
		status, secure, err := checkLoopback(ctx, host, port, dial)
		if err != nil {
			continue
		}
		scheme, flag := "http", "-i"
		if secure {
			scheme, flag = "https", "-ik"
		}
		url := scheme + "://localhost:" + port + "/"
		_, err = fmt.Fprintf(out, "%s\n  status: %d %s\n  time: %s\n  curl:   curl %s %s\n",
			url, status, http.StatusText(status), time.Since(started).Round(time.Millisecond), flag, url)
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("local HTTP check: %w", err)
	}
	return errors.New("no valid bounded HTTP response from the local port")
}

func checkLoopback(ctx context.Context, host, port string, dial healthDialFunc) (int, bool, error) {
	deadline, _ := ctx.Deadline()
	address := net.JoinHostPort(host, port)
	conn, err := dial(ctx, "tcp", address)
	if err != nil {
		return 0, false, err
	}
	probeDeadline := time.Now().Add(healthProbeTimeout)
	if deadline.Before(probeDeadline) {
		probeDeadline = deadline
	}
	if err = conn.SetDeadline(probeDeadline); err != nil {
		_ = conn.Close()
		return 0, false, err
	}
	// Self-signed certificates remain supported for loopback diagnostics only.
	// No configured hostname, proxy or redirect can change the dialed address.
	tlsConn := tls.Client(conn, &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         "localhost",
		InsecureSkipVerify: true, // #nosec G402 -- literal loopback-only diagnostic
	})
	secure := tlsConn.HandshakeContext(ctx) == nil
	if secure {
		conn = tlsConn
	} else {
		_ = conn.Close()
		conn, err = dial(ctx, "tcp", address)
		if err != nil {
			return 0, false, err
		}
	}
	defer func() { _ = conn.Close() }()
	if err = conn.SetDeadline(deadline); err != nil {
		return 0, false, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:"+port+"/", nil)
	if err != nil {
		return 0, false, err
	}
	request.Close = true
	if err = request.Write(conn); err != nil {
		return 0, false, err
	}
	// One budget covers every interim and final header. Standard-library HTTP
	// parsing is retained; any bounded body read-ahead is discarded on return.
	reader := bufio.NewReaderSize(&io.LimitedReader{R: conn, N: healthMaxHeaderBytes}, 4096)
	for interim := 0; ; interim++ {
		response, readErr := http.ReadResponse(reader, request)
		if readErr != nil {
			return 0, false, readErr
		}
		if response.StatusCode >= 200 {
			// Close the socket first: Body.Close may otherwise drain a hostile
			// stream while trying to make a connection reusable.
			_ = conn.Close()
			_ = response.Body.Close()
		}
		if response.ProtoMajor != 1 || response.ProtoMinor > 1 ||
			response.StatusCode < 100 || response.StatusCode > 599 || response.StatusCode == 101 {
			return 0, false, errors.New("invalid HTTP response")
		}
		if response.StatusCode >= 200 {
			return response.StatusCode, secure, nil
		}
		if interim >= healthMaxInterimResponses {
			return 0, false, errors.New("too many interim HTTP responses")
		}
	}
}
