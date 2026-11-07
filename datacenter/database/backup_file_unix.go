//go:build !windows

package database

import "os"

func createPrivateBackup(path string) (*os.File, error) {
	// #nosec G304 -- Operator-selected absolute destination; exclusive create prevents replacing any existing path.
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}
