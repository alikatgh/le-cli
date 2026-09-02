//go:build !windows

package tools

import (
	"errors"
	"syscall"
)

// probeProcess is kill(pid, 0): a signal of zero delivers nothing but still
// performs the existence and permission checks, which is exactly the answer
// WatchPID needs. ESRCH means gone; EPERM means alive but not ours.
func probeProcess(pid int) error {
	err := syscall.Kill(pid, 0)
	switch {
	case errors.Is(err, syscall.ESRCH):
		return errProcessGone
	case errors.Is(err, syscall.EPERM):
		return errProcessForbidden
	}
	return err
}
