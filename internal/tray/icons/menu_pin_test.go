package icons

import (
	"image/color"
	"testing"
)

func TestMenuPinIsVisibleAndNeutral(t *testing.T) {
	for _, palette := range []TrayPalette{TrayPaletteDark, TrayPaletteLight} {
		img := decodePNG(t, MenuPin(palette))
		if img.Bounds().Dx() != 16 || img.Bounds().Dy() != 16 {
			t.Fatalf("%v: pin bounds = %v, want 16x16", palette, img.Bounds())
		}
		want := uint8(0x2b)
		if palette == TrayPaletteDark {
			want = 0xee
		}
		visible := 0
		for y := 0; y < MenuIconSize; y++ {
			for x := 0; x < MenuIconSize; x++ {
				c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				if c.A == 0 {
					continue
				}
				visible++
				if c.R != c.G || c.G != c.B {
					t.Fatalf("%v: colored pixel at (%d, %d): %v", palette, x, y, c)
				}
				if c.A == 255 && c.R != want {
					t.Fatalf("%v: pin does not use menu text color: %v", palette, c)
				}
			}
		}
		if visible < 20 {
			t.Fatalf("%v: pin has only %d visible pixels", palette, visible)
		}
		if countRedDominantPixels(img) != 0 {
			t.Fatalf("%v: pin has red risk pixels", palette)
		}
	}
}
