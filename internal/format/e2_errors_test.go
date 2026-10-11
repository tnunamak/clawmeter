package format

import (
	"fmt"
	"testing"
)

func TestHumanizeErrorPortsRemainNetwork(t *testing.T) {
	for _, port := range []int{40123, 40321, 42999} {
		input := fmt.Sprintf("request failed: network read failed at 127.0.0.1:%d", port)
		want := fmt.Sprintf("network read failed at 127.0.0.1:%d", port)
		if got := HumanizeError(input); got != want {
			t.Errorf("port %d: got %q, want %q", port, got, want)
		}
	}
}
