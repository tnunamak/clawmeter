//go:build linux || freebsd || openbsd || netbsd

package systray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestP2MissingSessionBusReturns(t *testing.T) {
	if os.Getenv("CLAWMETER_P2_NO_BUS") == "1" {
		var ready atomic.Int32
		Run(func() { ready.Add(1) }, func() {})
		fmt.Printf("ready:%d\n", ready.Load())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestP2MissingSessionBusReturns$")
	cmd.Env = append(os.Environ(), "CLAWMETER_P2_NO_BUS=1", "GORACE=atexit_sleep_ms=0", "DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/clawmeter-synthetic-bus")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("failed startup left native loop waiting: %s", out)
	}
	if err != nil || !strings.Contains(string(out), "ready:0") {
		t.Fatalf("failed startup reported ready or failed shutdown: %s %v", out, err)
	}
}
