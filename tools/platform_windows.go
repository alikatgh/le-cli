//go:build windows

package tools

// rundll32 url.dll,FileProtocolHandler hands the URL to the default browser
// WITHOUT a shell in between. The tempting `cmd /c start <url>` is avoided on
// purpose: cmd parses the URL, so an `&` in a query string would be read as a
// command separator. This form takes the URL as one discrete argument.
func browserCommand(url string) (string, []string) {
	return "rundll32", []string{"url.dll,FileProtocolHandler", url}
}

var (
	flushDNSExe  = "ipconfig"
	flushDNSArgs = []string{"/flushdns"}
)
