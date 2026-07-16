package provider

// Maturity is the deliberately binary confidence classification for a
// provider integration. Numeric confidence is intentionally not part of the
// product model: promotion to ordinary is a reviewed metadata change.
type Maturity string

const (
	MaturityOrdinary     Maturity = "ordinary"
	MaturityExperimental Maturity = "experimental"
)

// ProviderMaturity is stable provider metadata intended for inventory and
// machine-readable output, not primary quota rows.
type ProviderMaturity struct {
	Level     Maturity `json:"level"`
	LearnMore string   `json:"learn_more"`
}

const providerMaturityLearnMore = "docs/provider-maturity.md"

var providerMaturityByName = map[string]Maturity{
	"claude":     MaturityOrdinary,
	"openai":     MaturityOrdinary,
	"gemini":     MaturityOrdinary,
	"xai":        MaturityOrdinary,
	"kimi":       MaturityExperimental,
	"kimik2":     MaturityExperimental,
	"copilot":    MaturityExperimental,
	"openrouter": MaturityExperimental,
	"jetbrains":  MaturityExperimental,
	"synthetic":  MaturityExperimental,
	"zai":        MaturityExperimental,
}

// GetMaturity returns the conservative audit classification for a known
// provider. Unknown providers remain experimental until deliberately reviewed.
func GetMaturity(name string) ProviderMaturity {
	level, ok := providerMaturityByName[name]
	if !ok {
		// Unknown integrations are not promoted by accident, but this fallback
		// does not make an unassessed known provider experimental: every current
		// provider is required to have an explicit table entry and test coverage.
		level = MaturityExperimental
	}
	return ProviderMaturity{Level: level, LearnMore: providerMaturityLearnMore}
}
