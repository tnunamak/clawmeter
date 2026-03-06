# Clawmeter Architecture

This document describes the modular, extensible architecture for supporting multiple AI service providers.

## Overview

The new architecture is built around the **Provider** interface, allowing easy addition of new AI services without modifying core code.

```
┌─────────────────────────────────────────────────────────────┐
│                         CLI / Tray                          │
└───────────────────────┬─────────────────────────────────────┘
                        │
┌───────────────────────▼─────────────────────────────────────┐
│                   Provider Registry                         │
│         (discovers and manages all providers)               │
└───────────────────────┬─────────────────────────────────────┘
                        │
        ┌───────────────┼───────────────┐
        │               │               │
┌───────▼──────┐ ┌──────▼──────┐ ┌──────▼──────┐
│   Claude/    │ │   OpenAI    │ │   Gemini    │  ...
│  Anthropic   │ │  (future)   │ │  (future)   │
└──────────────┘ └─────────────┘ └─────────────┘
```

## Core Components

### 1. Provider Interface (`internal/provider/provider.go`)

All providers implement this interface:

```go
type Provider interface {
    Name() string                    // Unique identifier (e.g., "claude")
    DisplayName() string             // Human-readable name (e.g., "Claude")
    IsConfigured() bool              // Check if credentials are available
    FetchUsage(ctx context.Context) (*UsageData, error)
}
```

### 2. Usage Data (`internal/provider/provider.go`)

Standardized usage information:

```go
type UsageData struct {
    Provider    string        // Provider name
    FetchedAt   time.Time     // When data was fetched
    Windows     []UsageWindow // Usage windows (providers may have 1+)
    IsExpired   bool          // Credentials expired
    Error       string        // Error message if fetch failed
}

type UsageWindow struct {
    Name        string    // e.g., "5h", "daily"
    DisplayName string    // e.g., "5 hours"
    Utilization float64   // 0-100 percentage
    ResetsAt    time.Time // When limit resets
}
```

### 3. Provider Registry (`internal/provider/provider.go`)

Manages all available providers:

```go
registry := provider.NewRegistry()
registry.Register(claudeProvider)
registry.Register(openaiProvider)

// Fetch from all configured providers in parallel
results := provider.FetchAllParallel(ctx, registry)
```

### 4. Configuration (`internal/config/config.go`)

YAML-based configuration at `~/.config/clawmeter/config.yaml`:

```yaml
providers:
  claude:
    enabled: true
  openai:
    enabled: true
    api_key: sk-...
  gemini:
    enabled: false

settings:
  poll_interval: 300
  notification_thresholds:
    warning: 80
    critical: 95
```

### 5. Cache (`internal/cache/cache.go`)

Multi-provider aware caching:

```go
// Cache stores data for all providers
entry := &cache.Entry{
    ProviderData: map[string]*provider.UsageData{
        "claude": claudeData,
        "openai": openaiData,
    },
}

cache.Write(result)
```

## Adding a New Provider

To add a new provider (e.g., OpenAI):

### 1. Create Provider Implementation

Create `internal/provider/openai/openai.go`:

```go
package openai

import (
    "context"
    "github.com/tnunamak/clawmeter/internal/config"
    "github.com/tnunamak/clawmeter/internal/provider"
)

type Provider struct {
    cfg       config.ProviderConfig
    globalCfg *config.Config
}

func New(cfg config.ProviderConfig, globalCfg *config.Config) *Provider {
    return &Provider{cfg: cfg, globalCfg: globalCfg}
}

func (p *Provider) Name() string {
    return "openai"
}

func (p *Provider) DisplayName() string {
    return "OpenAI"
}

func (p *Provider) IsConfigured() bool {
    return p.cfg.APIKey != "" || os.Getenv("OPENAI_API_KEY") != ""
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
    // Implementation here
    // 1. Get API key from config or env
    // 2. Call OpenAI API (e.g., to get rate limit headers)
    // 3. Transform to UsageData format
    
    return &provider.UsageData{
        Provider:  p.Name(),
        FetchedAt: time.Now(),
        Windows: []provider.UsageWindow{
            {
                Name:        "rpm",
                DisplayName: "Requests/min",
                Utilization: calculateRpmUsage(),
                ResetsAt:    time.Now().Add(time.Minute),
            },
            {
                Name:        "tpm",
                DisplayName: "Tokens/min",
                Utilization: calculateTpmUsage(),
                ResetsAt:    time.Now().Add(time.Minute),
            },
        },
    }, nil
}

func Register(registry *provider.Registry, cfg *config.Config) error {
    providerCfg, _ := cfg.GetProvider("openai")
    return registry.Register(New(providerCfg, cfg))
}
```

### 2. Register in CLI (`cmd/clawmeter/main.go`)

Add to the registry setup:

```go
import "github.com/tnunamak/clawmeter/internal/provider/openai"

// In statusCmd or initialization:
registry := provider.NewRegistry()
anthropic.Register(registry, cfg)
openai.Register(registry, cfg)  // Add this
```

### 3. Register in Tray (`internal/tray/tray.go`)

Same registration in `onReady()`:

```go
registry := provider.NewRegistry()
anthropic.Register(registry, cfg)
openai.Register(registry, cfg)  // Add this
```

### 4. Update Provider List (`cmd/clawmeter/main.go`)

Add to `isProviderCommand()` and `providersCmd()`:

```go
func isProviderCommand(cmd string) bool {
    knownProviders := []string{"claude", "openai", "gemini", "kimi", ...}
    // ...
}
```

## Provider Auto-Discovery

Providers can auto-enable if credentials are detected:

```go
func (p *Provider) IsConfigured() bool {
    if !p.cfg.Enabled {
        // Auto-enable if we can find credentials
        _, err := p.findCredentials()
        return err == nil
    }
    return p.findCredentials() == nil
}
```

This allows the app to "just work" if Claude Code is installed, without explicit configuration.

## Parallel Fetching

All configured providers are fetched concurrently:

```go
results := provider.FetchAllParallel(ctx, registry)

for name, data := range results.Results {
    if data.Error != "" {
        log.Printf("%s failed: %s", name, data.Error)
        continue
    }
    displayUsage(data)
}
```

## Display Format

### CLI Color Output

```
Claude    5h ███░░░░░░░░░░░░░░░░░  17%  resets 3h05m  ✓
          7d ████████████░░░░░░░░  60%  resets 1d7h   ✓

OpenAI   rpm ████████████████░░░░  80%  resets 45s    ⚠
         tpm ████████░░░░░░░░░░░░  42%  resets 45s    ✓
```

### Tray

- **Title**: Compact summary (e.g., "5h:17% 7d:60%")
- **Menu**: Provider sections with window details
- **Tooltip**: Full multi-line breakdown
- **Icon color**: Based on worst projected usage across all providers

## Testing a Provider

Test individual providers:

```bash
# Show all providers
clawmeter

# Show specific provider
clawmeter openai
clawmeter openai --json

# List provider status
clawmeter providers

# Enable/disable
clawmeter config enable openai
clawmeter config disable claude
```

## Error Handling

Each provider's errors are isolated:

- One provider failing doesn't break others
- Errors are displayed inline with provider name
- Expired tokens show special "expired" state
- Gray icon indicates no healthy providers

## Backward Compatibility

The existing `CLAUDE_CODE_OAUTH_TOKEN` environment variable and credential locations continue to work. The Anthropic provider auto-discovers these.

## Future Enhancements

1. **Plugin system**: Load providers from external `.so` files
2. **Custom providers**: User-defined providers via config
3. **Aggregated limits**: Track combined usage across similar services
4. **Budget tracking**: Track spend across paid providers
5. **Historical data**: Store and graph usage over time
