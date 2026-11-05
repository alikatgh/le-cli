//go:build windows

package companion

import (
	"context"
	"errors"
	"io"
)

func Run(ctx context.Context, version string, out io.Writer) error {
	return errors.New("companion is only available on macOS")
}
