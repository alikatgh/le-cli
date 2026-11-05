//go:build windows

package boundedexec

import "os/exec"

// The companion is macOS-only. Windows keeps compilation compatibility; no
// companion route is registered there and the legacy collectors are unchanged.
func configure(cmd *exec.Cmd) {}
func retire(cmd *exec.Cmd)    {}
