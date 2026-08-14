// Package all registers every known provider with a registry.
package all

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/alibaba"
	"github.com/tnunamak/clawmeter/internal/provider/alibabatoken"
	"github.com/tnunamak/clawmeter/internal/provider/anthropic"
	"github.com/tnunamak/clawmeter/internal/provider/antigravity"
	"github.com/tnunamak/clawmeter/internal/provider/copilot"
	"github.com/tnunamak/clawmeter/internal/provider/deepseek"
	"github.com/tnunamak/clawmeter/internal/provider/gemini"
	"github.com/tnunamak/clawmeter/internal/provider/jetbrains"
	"github.com/tnunamak/clawmeter/internal/provider/kimi"
	"github.com/tnunamak/clawmeter/internal/provider/kimik2"
	"github.com/tnunamak/clawmeter/internal/provider/openai"
	"github.com/tnunamak/clawmeter/internal/provider/openrouter"
	"github.com/tnunamak/clawmeter/internal/provider/synthetic"
	"github.com/tnunamak/clawmeter/internal/provider/xai"
	"github.com/tnunamak/clawmeter/internal/provider/zai"
)

var aliases = map[string]string{
	"codex":              "openai",
	"grok":               "xai",
	"x.ai":               "xai",
	"x-ai":               "xai",
	"deep-seek":          "deepseek",
	"xai":                "xai",
	"openai":             "openai",
	"qwen":               "alibaba",
	"bailian":            "alibaba",
	"dashscope":          "alibaba",
	"token-plan":         "alibaba_token",
	"alibaba-token-plan": "alibaba_token",
	"alibaba-token":      "alibaba_token",
	"bailian-token-plan": "alibaba_token",
}

type registration struct {
	name string
	new  func(config.ProviderConfig) provider.Provider
}

var registrations = []registration{
	{name: "alibaba", new: func(cfg config.ProviderConfig) provider.Provider { return alibaba.New(cfg) }},
	{name: "alibaba_token", new: func(cfg config.ProviderConfig) provider.Provider { return alibabatoken.New(cfg) }},
	{name: "antigravity", new: func(config.ProviderConfig) provider.Provider { return antigravity.New() }},
	{name: "kimi", new: func(cfg config.ProviderConfig) provider.Provider { return kimi.New(cfg) }},
	{name: "kimik2", new: func(cfg config.ProviderConfig) provider.Provider { return kimik2.New(cfg) }},
	{name: "openai", new: func(cfg config.ProviderConfig) provider.Provider { return openai.New(cfg) }},
	{name: "gemini", new: func(cfg config.ProviderConfig) provider.Provider { return gemini.New(cfg) }},
	{name: "copilot", new: func(cfg config.ProviderConfig) provider.Provider { return copilot.New(cfg) }},
	{name: "deepseek", new: func(cfg config.ProviderConfig) provider.Provider { return deepseek.New(cfg) }},
	{name: "openrouter", new: func(cfg config.ProviderConfig) provider.Provider { return openrouter.New(cfg) }},
	{name: "jetbrains", new: func(cfg config.ProviderConfig) provider.Provider { return jetbrains.New(cfg) }},
	{name: "synthetic", new: func(cfg config.ProviderConfig) provider.Provider { return synthetic.New(cfg) }},
	{name: "xai", new: func(cfg config.ProviderConfig) provider.Provider { return xai.New(cfg) }},
	{name: "zai", new: func(cfg config.ProviderConfig) provider.Provider { return zai.New(cfg) }},
	{name: "claude", new: func(cfg config.ProviderConfig) provider.Provider { return anthropic.New(cfg) }},
}

// Register registers all known providers with the given registry and wires
// the user's config as the registry's enabled-filter so explicitly disabled
// providers are skipped by GetConfigured / FetchAllParallel.
func Register(registry *provider.Registry, cfg *config.Config, resolvers ...provider.SessionEnvironmentResolver) {
	registry.SetEnabledFilter(cfg)
	if len(resolvers) > 0 {
		registry.SetSessionEnvironmentResolver(resolvers[0])
	}
	for _, registration := range registrations {
		providerCfg := cfg.Providers[registration.name]
		base := registration.new(providerCfg)
		if err := provider.RegisterConfigured(registry, providerCfg, base); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: provider registration: %v\n", err)
		}
	}
}

// SourceCapability returns the provider-owned enrollment capability, if any.
func SourceCapability(name string) (provider.SourceCapability, bool) {
	canonical, ok := canonicalRegistrationName(name)
	if !ok {
		return nil, false
	}
	for _, registration := range registrations {
		if registration.name != canonical {
			continue
		}
		return provider.SourceCapabilityOf(registration.new(config.ProviderConfig{}))
	}
	return nil, false
}

// SourceValidator assembles provider-specific validation without making
// config depend on provider implementations.
func SourceValidator() config.SourceValidator {
	return func(family string, sources []config.SourceConfig) error {
		capability, ok := SourceCapability(family)
		if !ok {
			return fmt.Errorf("provider %q does not support enrolled sources", family)
		}
		if err := provider.ValidateSourceConfigs(capability, sources); err != nil {
			return fmt.Errorf("provider %q sources: %w", family, err)
		}
		return nil
	}
}

func canonicalRegistrationName(name string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if alias, ok := aliases[normalized]; ok {
		return alias, true
	}
	for _, registration := range registrations {
		if registration.name == normalized {
			return normalized, true
		}
	}
	return "", false
}

// Names returns the canonical names of every known provider, sorted.
// This is the source of truth the CLI uses to validate `config enable/disable`
// arguments without paying for full registry construction.
func Names() []string {
	reg := provider.NewRegistry()
	Register(reg, config.DefaultConfig())
	all := reg.GetAll()
	names := make([]string, 0, len(all))
	for _, p := range all {
		names = append(names, p.Name())
	}
	sort.Strings(names)
	return names
}

// IsKnown reports whether name is a registered provider key or accepted alias.
func IsKnown(name string) bool {
	_, ok := CanonicalName(name)
	return ok
}

// IsCanonicalName reports whether name is the stable provider key used in
// config/cache files.
func IsCanonicalName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return false
	}
	for _, n := range Names() {
		if n == normalized {
			return true
		}
	}
	return false
}

// CanonicalName maps a user-facing provider name or legacy config key to the
// stable provider key used in config/cache files.
func CanonicalName(name string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return "", false
	}
	if alias, ok := aliases[normalized]; ok {
		return alias, true
	}
	for _, n := range Names() {
		if n == normalized {
			return n, true
		}
	}
	return "", false
}

// Suggest returns the closest known provider name to input, or "" if no
// close match exists. Uses simple Levenshtein distance with a cap so we
// don't suggest wildly different names for total typos.
func Suggest(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return ""
	}
	if canonical, ok := CanonicalName(input); ok {
		return canonical
	}
	best := ""
	bestDist := -1
	// Cap distance roughly proportional to input length so "opneai" → "openai"
	// (distance 2) matches, but a totally unrelated word doesn't.
	maxDist := len(input) / 2
	if maxDist < 2 {
		maxDist = 2
	}
	candidates := append(Names(), "codex", "grok")
	for _, n := range candidates {
		d := levenshtein(input, n)
		if d > maxDist {
			continue
		}
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = n
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	la := len(ar)
	lb := len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
