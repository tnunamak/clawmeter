package main

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type writeFailurePNG struct{ *os.File }

func (*writeFailurePNG) Write([]byte) (int, error) {
	return 0, errors.New("synthetic encode failure")
}

func TestPNGEncodeFailurePreservesExistingSheet(t *testing.T) {
	const destination = "existing.png"
	if os.Getenv("CLAWMETER_PNG_ATOMIC_CHILD") == "1" {
		createPNGFile = func(path string) (io.WriteCloser, error) {
			f, err := os.Create(path)
			if err != nil {
				return nil, err
			}
			return &writeFailurePNG{f}, nil
		}
		writePNG(destination, image.NewRGBA(image.Rect(0, 0, 2, 2)))
		return
	}

	dir := t.TempDir()
	var seed bytes.Buffer
	if err := png.Encode(&seed, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, destination)
	if err := os.WriteFile(path, seed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPNGEncodeFailurePreservesExistingSheet$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLAWMETER_PNG_ATOMIC_CHILD=1", "GORACE=atexit_sleep_ms=0")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), "synthetic encode failure") {
		t.Fatalf("expected encode failure, got %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, seed.Bytes()) {
		t.Fatalf("prior PNG changed or became unreadable: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != destination {
		t.Fatalf("temporary file left behind: %v, %v", entries, err)
	}
}

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
