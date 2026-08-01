//go:build tray

package tray

import (
	"bytes"
	"testing"

	"github.com/tnunamak/clawmeter/internal/tray/icons"
)

func TestDynamicIconDataUsesNativeProviderRenderer(t *testing.T) {
	meter := icons.MeterState{UsagePct: 68, ExpectedPct: 35, ShowExpected: true, Label: "7D"}
	want := icons.GenerateProviderIconWithMeter("openai", meter, 32)
	got := dynamicIconData("openai", meter, []byte("deliberately ignored legacy 128px raster"), 32)
	if !bytes.Equal(got, want) {
		t.Fatal("32px Linux pixmap was not rendered from the native icon geometry")
	}
}

func TestDynamicIconSizesLeadWithNativeV10Rasters(t *testing.T) {
	want := []int{22, 32, 64, 128}
	if len(dynamicIconSizes) != len(want) {
		t.Fatalf("dynamic icon sizes = %v, want %v", dynamicIconSizes, want)
	}
	for i, size := range want {
		if dynamicIconSizes[i] != size {
			t.Fatalf("dynamic icon sizes = %v, want %v", dynamicIconSizes, want)
		}
	}
}
