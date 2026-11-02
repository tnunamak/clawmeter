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

func TestNativeEndWithoutDBusConnection(t *testing.T) {
	systrayExit = func() {}
	systrayExitCalled.Store(false)
	instance.conn = nil
	nativeEnd() // must not panic when session bus setup failed
}

func TestSeparatorItemHidesAndShows(t *testing.T) {
	separator := AddSeparatorItem()
	visible := func() any {
		instance.menuLock.Lock()
		defer instance.menuLock.Unlock()
		layout, ok := findLayout(int32(separator.id))
		if !ok {
			t.Fatal("separator not in the menu layout")
		}
		if v, ok := layout.V1["visible"]; ok {
			return v.Value()
		}
		return true
	}
	separator.Hide()
	if visible() != false {
		t.Fatal("hidden separator is still visible")
	}
	separator.Show()
	if visible() != true {
		t.Fatal("shown separator is still hidden")
	}
}

func TestTooltipPropertySeparatesTitleAndLiteralBody(t *testing.T) {
	cases := []struct {
		name, input, title, body string
	}{
		{"empty", "", "", ""},
		{"single", "Quota <literal>", "Quota <literal>", ""},
		{"empty body", "Quota\n", "Quota", ""},
		{"two lines", "Quota\n92% used", "Quota", "<p><b>92% used</b></p>"},
		{"multiple", "Quota\n92% used\nReset tomorrow\n", "Quota", "<p><b>92% used</b><br/>Reset tomorrow<br/></p>"},
		{"unicode", "账户 🌙\n已用 ۹۲٪\n明天", "账户 🌙", "<p><b>已用 ۹۲٪</b><br/>明天</p>"},
		{"literal markup", "Title <plain>\n<b>A&B</b> \"quoted\" 'single'\n<script>x</script>", "Title <plain>", "<p><b>&lt;b&gt;A&amp;B&lt;/b&gt; &#34;quoted&#34; &#39;single&#39;</b><br/>&lt;script&gt;x&lt;/script&gt;</p>"},
		{"blank first body line", "Quota\n\nLater", "Quota", "<p><b></b><br/>Later</p>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &tray{tooltipTitle: tc.input}
			property := tr.createPropSpec()["org.kde.StatusNotifierItem"]["ToolTip"]
			got, ok := property.Value.(tooltip)
			if !ok {
				t.Fatalf("ToolTip value type = %T", property.Value)
			}
			if got.V2 != tc.title || got.V3 != tc.body || got.V0 != "" || len(got.V1) != 0 {
				t.Fatalf("ToolTip = %#v, want title %q body %q and no icon", got, tc.title, tc.body)
			}
			if !property.Writable || property.Callback != nil {
				t.Fatal("ToolTip property contract changed")
			}
			if tr.tooltipTitle != tc.input {
				t.Fatal("original text cache changed")
			}
		})
	}
}

func TestSetTooltipCachesOriginalBeforeRegistration(t *testing.T) {
	previous := instance
	instance = &tray{}
	t.Cleanup(func() { instance = previous })
	for _, text := range []string{"Account\n92% used\nReset tomorrow", "Other\n<stale>&", ""} {
		SetTooltip(text)
		if instance.tooltipTitle != text {
			t.Fatalf("cached text = %q, want %q", instance.tooltipTitle, text)
		}
		got := instance.createPropSpec()["org.kde.StatusNotifierItem"]["ToolTip"].Value.(tooltip)
		if text == "Other\n<stale>&" && (got.V2 != "Other" || got.V3 != "<p><b>&lt;stale&gt;&amp;</b></p>") {
			t.Fatalf("updated tooltip = %#v", got)
		}
	}
}
