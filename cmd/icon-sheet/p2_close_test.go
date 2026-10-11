package main

import (
	"bytes"
	"errors"
	"image"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type closeFailurePNG struct{ bytes.Buffer }

func (*closeFailurePNG) Close() error { return errors.New("synthetic close failure") }
func TestP2PNGCloseFailure(t *testing.T) {
	if os.Getenv("CLAWMETER_P2_PNG_CHILD") == "1" {
		createPNGFile = func(string) (io.WriteCloser, error) { return &closeFailurePNG{}, nil }
		writePNG("synthetic.png", image.NewRGBA(image.Rect(0, 0, 1, 1)))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestP2PNGCloseFailure$")
	cmd.Env = append(os.Environ(), "CLAWMETER_P2_PNG_CHILD=1", "GORACE=atexit_sleep_ms=0")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "synthetic close failure") {
		t.Fatalf("close failure reported success: %s %v", out, err)
	}
}
