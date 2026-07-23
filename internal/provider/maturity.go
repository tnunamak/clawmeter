package provider

// ProviderMaturity is binary provider metadata intended for inventory and
// machine-readable output, not primary quota rows. Numeric confidence and a
// named "stable" tier are intentionally not part of the product model.
type ProviderMaturity struct {
	Experimental bool   `json:"experimental"`
	LearnMore    string `json:"learn_more,omitempty"`
}

const providerMaturityLearnMore = "https://github.com/tnunamak/clawmeter/blob/main/docs/provider-maturity.md"

var experimentalProviderByName = map[string]bool{
	"antigravity": true,
	"claude":      false,
	"openai":      false,
	"gemini":      false,
	"xai":         true,
	"kimi":        true,
	"kimik2":      true,
	"copilot":     true,
	"openrouter":  true,
	"jetbrains":   true,
	"synthetic":   true,
	"zai":         true,
}

// GetMaturity returns the conservative audit classification for a known
// provider. Unknown providers remain experimental until deliberately reviewed.
func GetMaturity(name string) ProviderMaturity {
	experimental, ok := experimentalProviderByName[name]
	if !ok {
		experimental = true
	}
	result := ProviderMaturity{Experimental: experimental}
	if experimental {
		result.LearnMore = providerMaturityLearnMore
	}
	return result
}
