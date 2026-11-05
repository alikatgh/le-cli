//go:build windows

package kill

import (
	"context"
	"errors"
)

func startTimeContext(ctx context.Context, pid int) (string, error) {
	return "", errors.New("companion is only available on macOS")
}
