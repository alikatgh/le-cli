package tools

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/alikatgh/le-cli/scan"
)

// Check does an HTTP(S) GET against localhost:<port> and reports the status and
// round-trip time — a quick "is it actually responding?" beyond "a socket is
// open". The scheme is auto-detected via a TLS probe (vite --https / caddy
// answer on https only), and the host is localhost rather than 127.0.0.1 so
// IPv6-only listeners on [::1] are reachable too. It also prints a ready-to-run
// curl command for the URL (-k added for https: dev certs are self-signed). A
// listener that speaks a non-HTTP protocol (a database, say) surfaces as a
// clear connection/read error rather than hanging, thanks to the bounded
// timeout.
func Check(port string) error {
	if err := validPort(port); err != nil {
		return err
	}
	scheme := scan.Scheme(port)
	url := scheme + "://localhost:" + port + "/"
	client := &http.Client{Timeout: 5 * time.Second}
	curl := "curl -i " + url
	if scheme == "https" {
		// Localhost dev certs are self-signed; without this the health check
		// reports a cert error instead of the server's actual HTTP status.
		// Detection/diagnostics only — nothing from the response is trusted.
		client.Transport = &http.Transport{
			// #nosec G402 -- localhost health check against self-signed dev certs.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		curl = "curl -ik " + url
	}

	start := time.Now()
	resp, err := client.Get(url) // #nosec G107 -- fixed localhost URL, port is validated above
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		return fmt.Errorf("no HTTP response from %s after %s: %w", url, elapsed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	fmt.Printf("%s\n", url)
	fmt.Printf("  status: %s\n", resp.Status)
	fmt.Printf("  time:   %s\n", elapsed)
	fmt.Printf("  curl:   %s\n", curl)
	return nil
}
