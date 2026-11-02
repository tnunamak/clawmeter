//go:build linux

package systray

import "testing"

func TestStyledTitleKeepsTextAndSurvivesOtherUpdates(t *testing.T) {
	item := newMenuItem("old", "", nil)
	spans := []TextSpan{{Text: "📌 Work ", Style: TextNormal}, {Text: "█", Style: TextRed}, {Text: "┃", Style: TextBold}, {Text: "░", Style: TextGreen}}
	item.SetStyledTitle(spans)
	spans[1].Text = "poison"
	if item.title != "📌 Work █┃░" || item.titleSpans[1].Text != "█" {
		t.Fatal("native title aliases caller or loses text")
	}
	item.Disable()
	item.Enable()
	item.Hide()
	item.Show()
	if len(item.titleSpans) != 4 {
		t.Fatal("unrelated update lost native style")
	}
	item.SetTitle(item.title)
	if len(item.titleSpans) != 0 {
		t.Fatal("plain update left old colors")
	}
	item.Remove()
}
