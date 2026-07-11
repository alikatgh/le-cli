package intel

import "strings"

// Tunnel identities: kubectl port-forward sessions and cloudflared tunnels.
// Without these a forward shows up as an anonymous "kubectl" process — the
// user sees a mystery listener on :8080 with no hint that killing it severs a
// cluster connection. Both parse the command line we already capture.

// kubectlForwardIdentity extracts "K8s forward: svc/frontend 8080→80" from a
// `kubectl port-forward` command line. Falls back to a generic label when the
// target can't be parsed (truncated ps output, exotic flags).
func kubectlForwardIdentity(commandLine string) (identity, target string) {
	fields := strings.Fields(commandLine)
	// Locate the port-forward subcommand; everything before it is kubectl
	// itself plus global flags (--context, --kubeconfig, …).
	sub := -1
	for i, f := range fields {
		if f == "port-forward" {
			sub = i
			break
		}
	}
	if sub == -1 {
		return "K8s port-forward", ""
	}

	var ports []string
	namespace := ""
	for i := sub + 1; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "-n" || f == "--namespace":
			if i+1 < len(fields) {
				namespace = fields[i+1]
				i++
			}
		case strings.HasPrefix(f, "--namespace="):
			namespace = strings.TrimPrefix(f, "--namespace=")
		case strings.HasPrefix(f, "-"):
			// Some other flag; skip (and skip its value if separate — a wrong
			// guess here only costs identity detail, never correctness).
		case isPortMapping(f):
			ports = append(ports, strings.ReplaceAll(f, ":", "→"))
		case target == "":
			target = f // pod/deploy/svc name — the first non-flag arg
		}
	}

	if target == "" {
		return "K8s port-forward", ""
	}
	label := "K8s forward: " + target
	if len(ports) > 0 {
		label += " " + strings.Join(ports, " ")
	}
	if namespace != "" {
		label += " (" + namespace + ")"
	}
	return label, target
}

// isPortMapping reports whether s looks like kubectl's PORT or LOCAL:REMOTE
// (or [LOCAL]:REMOTE with empty local for a random port).
func isPortMapping(s string) bool {
	if s == "" {
		return false
	}
	colons := 0
	for _, r := range s {
		switch {
		case r == ':':
			colons++
			if colons > 1 {
				return false
			}
		case r < '0' || r > '9':
			return false
		}
	}
	return true
}

// cloudflaredIdentity labels a cloudflared process: a named tunnel
// (`cloudflared tunnel run <name>`), a quick tunnel (`--url <origin>`), or the
// generic daemon.
func cloudflaredIdentity(commandLine string) string {
	fields := strings.Fields(commandLine)
	for i, f := range fields {
		if f == "run" && i+1 < len(fields) && !strings.HasPrefix(fields[i+1], "-") {
			return "Cloudflare Tunnel: " + fields[i+1]
		}
		if f == "--url" && i+1 < len(fields) {
			return "Cloudflare quick tunnel → " + strings.TrimPrefix(strings.TrimPrefix(fields[i+1], "http://"), "https://")
		}
		if strings.HasPrefix(f, "--url=") {
			origin := strings.TrimPrefix(f, "--url=")
			return "Cloudflare quick tunnel → " + strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://")
		}
	}
	return "Cloudflare Tunnel"
}
