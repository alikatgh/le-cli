//go:build !windows

package kill

import (
	"context"
	"github.com/alikatgh/le-cli/internal/boundedexec"
	"strconv"
)

func startTimeContext(ctx context.Context, pid int) (string, error) {
	return boundedexec.Output(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
}
