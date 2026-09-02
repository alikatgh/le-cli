//go:build windows

package tools

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a process that
// has not exited (STATUS_PENDING). Spelled out rather than imported so the
// intent is on the page.
const stillActive = 259

// probeProcess opens the PID with the least privilege that still answers
// "does it exist" — PROCESS_QUERY_LIMITED_INFORMATION, which even protected
// and other-user processes grant. The error mapping mirrors kill(pid, 0):
// ERROR_INVALID_PARAMETER is Windows for "no such process", ERROR_ACCESS_DENIED
// for "exists, not yours". One extra check unix doesn't need: a PID whose
// handle something still holds can be opened after it has exited, so the exit
// code is consulted before calling it alive.
func probeProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
			return errProcessGone
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return errProcessForbidden
		}
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err == nil && code != stillActive {
		return errProcessGone
	}
	return nil
}
