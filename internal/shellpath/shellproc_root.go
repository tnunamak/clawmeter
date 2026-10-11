//go:build darwin || linux

package shellpath

import (
	"context"
	"errors"
	"syscall"
	"time"
)

const rootExitPollInterval = 5 * time.Millisecond

// probeRootExit is a seam so tests can inject probe errors.
var probeRootExit = rootProcessExited

func waitForRootExitOrContext(ctx context.Context, pid int) {
	ticker := time.NewTicker(rootExitPollInterval)
	defer ticker.Stop()

	for {
		exited, err := probeRootExit(pid)
		// EINTR means a signal (such as Go's preemption signal) interrupted
		// the probe, not that the root is gone: poll again. Any other error
		// ends the wait so cleanup stays bounded.
		if errors.Is(err, syscall.EINTR) {
			exited, err = false, nil
		}
		if err != nil || exited {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
