// Package all registers every known provider with a registry.
package all

import (
	"fmt"
	"os"

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
	"github.com/tnunamak/clawmeter/internal/provider/zai"
)

// Register registers all known providers with the given registry.
func Register(registry *provider.Registry, cfg *config.Config) {
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
		zai.Register,
	} {
		if err := fn(registry, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: provider registration: %v\n", err)
		}
	}
}
