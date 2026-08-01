package icons

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestV10DarkNativeReferencePixels(t *testing.T) {
	testV10ReferencePixels(t, "dark", TrayPaletteDark, color.NRGBA{R: 30, G: 34, B: 39, A: 255})
}

func TestV10LightNativeReferencePixels(t *testing.T) {
	testV10ReferencePixels(t, "light", TrayPaletteLight, color.NRGBA{R: 242, G: 244, B: 247, A: 255})
}

func TestV10AtlasContainsEveryMeterState(t *testing.T) {
	for _, size := range []int{22, 32} {
		for _, theme := range []frameTheme{frameThemeDark, frameThemeLight} {
			atlas, err := loadV10Atlas(size, theme)
			if err != nil {
				t.Fatal(err)
			}
			for usage := 0; usage <= 100; usage++ {
				row := atlas.files[usage]
				if row == nil {
					t.Fatalf("%dpx %s atlas is missing usage row %d", size, theme, usage)
				}
				reader, err := row.Open()
				if err != nil {
					t.Fatal(err)
				}
				img, err := png.Decode(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				if got, want := img.Bounds().Dx(), 101*size; got != want || img.Bounds().Dy() != size {
					t.Fatalf("%dpx %s row %d bounds = %v, want %dx%d", size, theme, usage, img.Bounds(), want, size)
				}
			}
		}
	}
}

func testV10ReferencePixels(t *testing.T, theme string, palette TrayPalette, background color.NRGBA) {
	t.Helper()
	states := []struct {
		name  string
		meter MeterState
	}{
		{"on", MeterState{UsagePct: 52, ExpectedPct: 52, ShowExpected: true, Label: "7D"}},
		{"under", MeterState{UsagePct: 30, ExpectedPct: 65, ShowExpected: true, Label: "7D"}},
		{"over-update", MeterState{UsagePct: 78, ExpectedPct: 40, ShowExpected: true, Label: "7D", UpdateAvailable: true}},
	}
	for _, size := range []int{22, 32} {
		for _, state := range states {
			t.Run(itoa(size)+"/"+state.name, func(t *testing.T) {
				name := "openai-" + itoa(size) + "-" + state.name
				if theme == "light" {
					name += "-light"
				}
				reference, err := os.ReadFile(filepath.Join("testdata", "v10", name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				want, err := png.Decode(bytes.NewReader(reference))
				if err != nil {
					t.Fatal(err)
				}
				got, err := png.Decode(bytes.NewReader(GenerateProviderIconWithMeterPalette("openai", state.meter, size, palette)))
				if err != nil {
					t.Fatal(err)
				}
				assertSamePixels(t, onV10Surface(want, background), onV10Surface(got, background))
			})
		}
	}
}

func onV10Surface(src image.Image, background color.NRGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Over)
	return dst
}

func assertSamePixels(t *testing.T, want, got image.Image) {
	t.Helper()
	for y := 0; y < want.Bounds().Dy(); y++ {
		for x := 0; x < want.Bounds().Dx(); x++ {
			wr, wg, wb, wa := want.At(x, y).RGBA()
			gr, gg, gb, ga := got.At(x, y).RGBA()
			if wr != gr || wg != gg || wb != gb || wa != ga {
				t.Fatalf("pixel %d,%d = (%d,%d,%d,%d), want (%d,%d,%d,%d)", x, y, gr>>8, gg>>8, gb>>8, ga>>8, wr>>8, wg>>8, wb>>8, wa>>8)
			}
		}
	}
}
