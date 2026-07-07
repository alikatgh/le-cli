package tools

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// QR prints this Mac's LAN URL for a local port — what to open on a phone on the
// same Wi-Fi — and renders a scannable QR in the terminal when `qrencode` is
// installed. It resolves the LAN IP because `localhost` on the phone points at
// the phone, not this machine. Mirrors the app's "Open on phone" tool.
//
// We shell out to qrencode rather than vendor a QR encoder so le stays
// dependency-free; the URL (the actual payload) always prints regardless.
func QR(port string) error {
	p, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("give a port 1-65535, e.g. `le qr 3000`")
	}

	primary := primaryLANIP()
	others := privateIPv4s()
	if primary == "" && len(others) == 0 {
		return fmt.Errorf("no LAN address found — are you on Wi-Fi or Ethernet?")
	}
	if primary == "" {
		primary = others[0]
	}
	url := fmt.Sprintf("http://%s:%d", primary, p)

	fmt.Printf("On a phone on the same network, open:\n\n  %s\n\n", url)

	extras := make([]string, 0, len(others))
	for _, ip := range others {
		if ip != primary {
			extras = append(extras, ip)
		}
	}
	if len(extras) > 0 {
		fmt.Println("Other addresses this Mac has (try these if the first won't load):")
		for _, ip := range extras {
			fmt.Printf("  http://%s:%d\n", ip, p)
		}
		fmt.Println()
	}

	renderQR(url)
	return nil
}

// primaryLANIP returns the IP of the default-route interface — the one traffic
// to the internet uses, which is normally the Wi-Fi/Ethernet LAN address a phone
// on the same network shares. No packets are sent: connecting a UDP socket only
// resolves the route locally.
func primaryLANIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		if ip4 := addr.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
			return ip4.String()
		}
	}
	return ""
}

// privateIPv4s lists every private, non-loopback IPv4 the machine has, so the
// user has fallbacks when the default route isn't the interface the phone shares
// (VPN up, multiple networks, etc.).
func privateIPv4s() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil && ip4.IsPrivate() && !ip4.IsLoopback() {
			out = append(out, ip4.String())
		}
	}
	return out
}

// renderQR draws a scannable QR for url when `qrencode` is on PATH; otherwise it
// points the user at the one-line install and leaves the printed URL as the way
// in.
func renderQR(url string) {
	path, err := exec.LookPath("qrencode")
	if err != nil {
		fmt.Println("Tip: `brew install qrencode` to show a scannable QR right here.")
		return
	}
	// #nosec G204 -- qrencode path comes from LookPath and url is a discrete exec argument (no shell).
	cmd := exec.Command(path, "-t", "ANSIUTF8", "-o", "-", url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("Tip: `brew install qrencode` to show a scannable QR right here.")
	}
}
