package systray

import "strings"

// TextStyle describes optional native emphasis; plain text is always retained.
type TextStyle uint8

const (
	TextNormal TextStyle = iota
	TextRed
	TextGreen
	TextMuted
	TextBold
)

type TextSpan struct {
	Text  string    `json:"text"`
	Style TextStyle `json:"style"`
}

// SetStyledTitle uses native attributed text where available. Backends without
// that capability render the same joined title through their ordinary API.
func (item *MenuItem) SetStyledTitle(spans []TextSpan) {
	item.titleSpans = append([]TextSpan(nil), spans...)
	var text strings.Builder
	for _, span := range spans {
		text.WriteString(span.Text)
	}
	item.title = text.String()
	item.update()
}
