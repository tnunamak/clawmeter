//go:build darwin || linux

package shellpath

import (
	"context"
	"time"
)

const rootExitPollInterval = 5 * time.Millisecond

func waitForRootExitOrContext(ctx context.Context, pid int) {
	ticker := time.NewTicker(rootExitPollInterval)
	defer ticker.Stop()

	for {
		exited, err := rootProcessExited(pid)
		if err != nil {
			return
		}
		if exited {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
