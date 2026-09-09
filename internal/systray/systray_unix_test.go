//go:build !windows && !darwin

package systray

import (
	"image"
	"image/color"
	"testing"
)

func TestARGBForImageUsesStraightAlphaChannels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	img.SetNRGBA(1, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})

	got := argbForImage(img)
	want := []byte{
		128, 200, 100, 50,
		255, 10, 20, 30,
	}
	if len(got) != len(want) {
		t.Fatalf("ARGB length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ARGB byte %d = %d, want %d", i, got[i], want[i])
		}
	}
}
