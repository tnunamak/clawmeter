package diagnose

import (
	"fmt"
	"testing"
)

func TestSafeErrorPortsRemainNetwork(t *testing.T) {
	for _, port := range []int{40123, 40321, 42999} {
		got, _ := safeError(fmt.Sprintf("network read failed at 127.0.0.1:%d", port))
		if got != "network" {
			t.Errorf("port %d: got %q, want network", port, got)
		}
	}
}
