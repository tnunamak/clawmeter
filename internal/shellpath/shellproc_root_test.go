//go:build darwin || linux

package shellpath

import (
	"context"
	"syscall"
	"testing"
	"time"
)

func TestWaitForRootExitRetriesInterruptedProbe(t *testing.T) {
	calls := 0
	probeRootExit = func(int) (bool, error) {
		calls++
		if calls <= 3 {
			return false, syscall.EINTR
		}
		return true, nil
	}
	t.Cleanup(func() { probeRootExit = rootProcessExited })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waitForRootExitOrContext(ctx, 1)
	if calls != 4 {
		t.Fatalf("probe calls = %d, want 4 (EINTR must not end the wait)", calls)
	}
	if ctx.Err() != nil {
		t.Fatal("wait ran until the context deadline")
	}
}
