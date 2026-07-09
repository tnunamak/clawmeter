// Package all registers every known provider with a registry.
package all

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/anthropic"
	"github.com/tnunamak/clawmeter/internal/provider/copilot"
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
	"codex":  "openai",
	"grok":   "xai",
	"x.ai":   "xai",
	"x-ai":   "xai",
	"xai":    "xai",
	"openai": "openai",
}

// Register registers all known providers with the given registry and wires
// the user's config as the registry's enabled-filter so explicitly disabled
// providers are skipped by GetConfigured / FetchAllParallel.
func Register(registry *provider.Registry, cfg *config.Config) {
	registry.SetEnabledFilter(cfg)
	for _, fn := range []func(*provider.Registry, *config.Config) error{
		anthropic.Register,
		kimi.Register,
		kimik2.Register,
		openai.Register,
		gemini.Register,
		copilot.Register,
		openrouter.Register,
		jetbrains.Register,
		synthetic.Register,
		xai.Register,
		zai.Register,
	} {
		if err := fn(registry, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: provider registration: %v\n", err)
		}
	}
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
