package tools

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// WatchPID blocks until process pid exits, polling kill(pid,0) once a second —
// the CLI half of the app's Notify-on-exit tool. It honours the same ESRCH/EPERM
// distinction as ExitWatcher: ESRCH means truly gone, EPERM means alive but
// owned by another user (so we can't reliably observe it). A positive timeout
// bounds the wait.
func WatchPID(pid int, timeout time.Duration) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	first := true
	for {
		err := syscall.Kill(pid, 0)
		switch {
		case errors.Is(err, syscall.ESRCH):
			if first {
				return fmt.Errorf("pid %d is not running", pid)
			}
			fmt.Printf("pid %d has exited\n", pid)
			return nil
		case errors.Is(err, syscall.EPERM):
			return fmt.Errorf("can't watch pid %d — it's owned by another user, so the OS won't report when it exits", pid)
		}
		// nil (alive) or any other errno (inconclusive) → keep polling.
		if first {
			fmt.Printf("watching pid %d — will report the moment it exits\n", pid)
			first = false
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return fmt.Errorf("pid %d still running after %s", pid, timeout)
		}
		time.Sleep(time.Second)
	}
}

// OpenWhenReady blocks until something is listening on port, then opens
// http://localhost:port/<path> in the browser — the app's Open-when-ready tool.
func OpenWhenReady(port, path string, timeout time.Duration) error {
	if err := WaitListening(port, timeout); err != nil {
		return err
	}
	url := "http://localhost:" + port + "/" + strings.TrimPrefix(path, "/")
	if err := exec.Command("/usr/bin/open", url).Run(); err != nil {
		return fmt.Errorf("port is ready but couldn't open %s: %w", url, err)
	}
	fmt.Println("opened " + url)
	return nil
}
