package kimik2

import "testing"

func TestExtractCreditsTracksNumericPresence(t *testing.T) {
	p := &Provider{}
	consumed, hasConsumed, remaining, hasRemaining := p.extractCredits(map[string]interface{}{
		"consumed":  float64(0),
		"remaining": float64(10),
	})
	if !hasConsumed || !hasRemaining || consumed != 0 || remaining != 10 {
		t.Fatalf("extractCredits() = %v/%t, %v/%t", consumed, hasConsumed, remaining, hasRemaining)
	}

	_, hasConsumed, _, hasRemaining = p.extractCredits(map[string]interface{}{"unrelated": float64(0)})
	if hasConsumed || hasRemaining {
		t.Fatal("absent credit values were reported as present")
	}
}
