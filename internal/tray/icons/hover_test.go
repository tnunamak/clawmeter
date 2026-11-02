package icons

import (
	"bytes"
	"encoding/xml"
	"github.com/tnunamak/clawmeter/internal/meter"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestHoverMeterSVGGeometry(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		used, projected, pace     float64
		hasPace                   bool
		usedCells, projectedCells int
	}{
		{"normal", 30, 70, 50, true, 3, 4},
		{"empty", 0, 0, 0, false, 0, 0},
		{"full", 100, 100, 100, true, 10, 0},
		{"overflow", 137, 250, 105, true, 10, 0},
		{"missing pace", 40, 80, 0, false, 4, 4},
		{"negative", -12, -1, -5, true, 0, 0},
		{"projection below use", 60, 20, 30, true, 6, 0},
		{"rounding", 25, 65, 45, true, 3, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, palette := range []TrayPalette{TrayPaletteDark, TrayPaletteLight} {
				got := HoverMeterSVG(meter.Bar(tc.used, tc.projected, tc.pace, tc.hasPace), palette)
				if !bytes.Equal(got, HoverMeterSVG(meter.Bar(tc.used, tc.projected, tc.pace, tc.hasPace), palette)) {
					t.Fatal("nondeterministic SVG")
				}
				d := xml.NewDecoder(bytes.NewReader(got))
				cells, solid, dots, ticks := 0, 0, 0, 0
				for {
					token, err := d.Token()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					e, ok := token.(xml.StartElement)
					if !ok {
						continue
					}
					a := map[string]string{}
					for _, attr := range e.Attr {
						a[attr.Name.Local] = attr.Value
					}
					switch e.Name.Local {
					case "svg":
						if a["width"] != "120" || a["height"] != "14" || a["viewBox"] != "0 0 120 14" {
							t.Fatalf("bad viewport: %v", a)
						}
					case "rect":
						num := func(key string) float64 {
							v, err := strconv.ParseFloat(a[key], 64)
							if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
								t.Fatalf("bad %s: %q", key, a[key])
							}
							return v
						}
						x, y, w, h := num("x"), num("y"), num("width"), num("height")
						margin := 0.0
						if a["stroke"] != "" {
							margin = 0.5
						}
						if x-margin < 0 || y-margin < 0 || x+w+margin > 120 || y+h+margin > 14 || w <= 0 || h <= 0 {
							t.Fatalf("out of bounds: %v", a)
						}
						if h == 12 {
							ticks++
						} else if w == 1 {
							dots++
						} else {
							cells++
							if w == 10 {
								solid++
							}
						}
						for k := range a {
							switch k {
							case "x", "y", "width", "height", "rx", "fill", "stroke", "stroke-width":
							default:
								t.Fatalf("unexpected attribute %q", k)
							}
						}
					default:
						t.Fatalf("unexpected element %q", e.Name.Local)
					}
				}
				wantTicks := 0
				if tc.hasPace {
					wantTicks = 1
				}
				if cells != 10 || solid != tc.usedCells || dots != tc.projectedCells*6 || ticks != wantTicks {
					t.Fatalf("cells/solid/dots/ticks = %d/%d/%d/%d", cells, solid, dots, ticks)
				}
				for _, forbidden := range []string{"href", "<script", "<style", "<text", "<image", "url(", "filter", "font"} {
					if strings.Contains(string(got), forbidden) {
						t.Fatalf("forbidden %s", forbidden)
					}
				}
			}
		})
	}
}

func TestHoverMeterSVGRejectsMalformedCells(t *testing.T) {
	for _, cells := range [][]meter.Cell{nil, {{Kind: meter.Used}}, {{Kind: meter.CellKind(99)}}} {
		if HoverMeterSVG(cells, TrayPaletteDark) != nil {
			t.Fatal("accepted invalid bar")
		}
	}
	cells := meter.Bar(30, 70, 50, true)
	cells = append(cells, meter.Cell{Kind: meter.Pace})
	if HoverMeterSVG(cells, TrayPaletteDark) != nil {
		t.Fatal("accepted duplicate pace")
	}
}

func TestHoverMeterSVGPaletteAndSemantics(t *testing.T) {
	dark := HoverMeterSVG(meter.Bar(70, 90, 50, true), TrayPaletteDark)
	light := HoverMeterSVG(meter.Bar(70, 90, 50, true), TrayPaletteLight)
	if bytes.Equal(dark, light) {
		t.Fatal("palette ignored")
	}
	if !bytes.Equal(dark, HoverMeterSVG(meter.Bar(70, 90, 50, true), TrayPalette("untrusted\"/>"))) {
		t.Fatal("unknown palette not dark default")
	}
	if strings.Count(string(dark), `fill="#ff8a80"`) != 2 {
		t.Fatal("ahead cells must follow meter.Bar")
	}
	behind := string(HoverMeterSVG(meter.Bar(20, 40, 60, true), TrayPaletteDark))
	if strings.Count(behind, `stroke="#81c995"`) != 4 {
		t.Fatal("behind projected and free cells missing")
	}
	if !bytes.Equal(HoverMeterSVG(meter.Bar(137, 250, 105, true), TrayPaletteDark), HoverMeterSVG(meter.Bar(100, 100, 100, true), TrayPaletteDark)) {
		t.Fatal("overflow rescaled")
	}
}

// The native tooltip label applies 0.75 opacity. Meter ink keeps 3:1 graphic
// contrast in both reference window themes; exact numbers remain native text.
func TestHoverMeterSemanticInkContrast(t *testing.T) {
	for _, tc := range []struct {
		palette TrayPalette
		bg      string
	}{{TrayPaletteDark, "#202326"}, {TrayPaletteLight, "#ffffff"}} {
		svg := string(HoverMeterSVG(meter.Bar(20, 80, 60, true), tc.palette)) + string(HoverMeterSVG(meter.Bar(70, 90, 50, true), tc.palette))
		colors := []string{"#ff8a80", "#81c995", "#d6dde4", "#f5f8fc"}
		if tc.palette == TrayPaletteLight {
			colors = []string{"#b3261e", "#137333", "#59636e", "#232930"}
		}
		rgb := func(hex string) []float64 {
			out := make([]float64, 3)
			for i := range 3 {
				v, err := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
				if err != nil {
					t.Fatal(err)
				}
				out[i] = float64(v) / 255
			}
			return out
		}
		luminance := func(c []float64) float64 {
			sum := 0.0
			for i, w := range []float64{.2126, .7152, .0722} {
				v := c[i]
				if v <= .04045 {
					v /= 12.92
				} else {
					v = math.Pow((v+.055)/1.055, 2.4)
				}
				sum += v * w
			}
			return sum
		}
		bg := rgb(tc.bg)
		low := luminance(bg)
		for _, color := range colors {
			if !strings.Contains(svg, color) {
				t.Fatalf("semantic ink %s missing", color)
			}
			ink := rgb(color)
			for i := range ink {
				ink[i] = .75*ink[i] + .25*bg[i]
			}
			high := luminance(ink)
			a, b := max(high, low), min(high, low)
			if ratio := (a + .05) / (b + .05); ratio < 3 {
				t.Fatalf("%s on %s: contrast %.2f", color, tc.bg, ratio)
			}
		}
	}
}
