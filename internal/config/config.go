// Package config handles configuration for multiple providers.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration.
type Config struct {
	// Providers configuration - keys are provider names (claude, openai, etc.)
	Providers map[string]ProviderConfig `yaml:"providers,omitempty"`
	
	// Global settings
	Settings GlobalSettings `yaml:"settings,omitempty"`
}

// ProviderConfig holds configuration for a single provider.
type ProviderConfig struct {
	// Enabled determines if this provider is active
	Enabled bool `yaml:"enabled"`
	
	// APIKey for services that use API key authentication
	APIKey string `yaml:"api_key,omitempty"`
	
	// OAuthToken for services that use OAuth
	OAuthToken string `yaml:"oauth_token,omitempty"`
	
	// Extra holds provider-specific configuration
	Extra map[string]interface{} `yaml:"extra,omitempty"`
}

// GlobalSettings holds application-wide settings.
type GlobalSettings struct {
	// PollInterval for the tray (in seconds)
	PollInterval int `yaml:"poll_interval,omitempty"`
	
	// NotificationThresholds for usage warnings
	NotificationThresholds NotificationConfig `yaml:"notification_thresholds,omitempty"`
}

// NotificationConfig holds notification settings.
type NotificationConfig struct {
	Warning  float64 `yaml:"warning,omitempty"`  // Default: 80%
	Critical float64 `yaml:"critical,omitempty"` // Default: 95%
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		Providers: make(map[string]ProviderConfig),
		Settings: GlobalSettings{
			PollInterval: 300, // 5 minutes
			NotificationThresholds: NotificationConfig{
				Warning:  80,
				Critical: 95,
			},
		},
	}
}

// GetProvider returns the config for a specific provider.
func (c *Config) GetProvider(name string) (ProviderConfig, bool) {
	pc, ok := c.Providers[name]
	return pc, ok
}

// configPath returns the path to the config file.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "clawmeter", "config.yaml"), nil
}

// Load reads configuration from the config file.
// Returns default config if file doesn't exist.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// Save writes configuration to the config file.
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// EnsureProvider creates or updates a provider config.
func (c *Config) EnsureProvider(name string, enabled bool) ProviderConfig {
	pc, exists := c.Providers[name]
	if !exists {
		pc = ProviderConfig{}
	}
	pc.Enabled = enabled
	c.Providers[name] = pc
	return pc
}
