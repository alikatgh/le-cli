package tools

import (
	"fmt"
	"net/http"
	"time"
)

// Check does an HTTP GET against 127.0.0.1:<port> and reports the status and
// round-trip time — a quick "is it actually responding?" beyond "a socket is
// open". It also prints a ready-to-run curl command for the URL. A listener
// that speaks a non-HTTP protocol (a database, say) surfaces as a clear
// connection/read error rather than hanging, thanks to the bounded timeout.
func Check(port string) error {
	if err := validPort(port); err != nil {
		return err
	}
	url := "http://127.0.0.1:" + port + "/"
	client := &http.Client{Timeout: 5 * time.Second}

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
	fmt.Printf("  curl:   curl -i %s\n", url)
	return nil
}
