// Package config handles configuration for multiple providers.
package config

import (
	"errors"
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

	// CheckForUpdates controls automatic GitHub release checks from the tray.
	CheckForUpdates *bool `yaml:"check_for_updates,omitempty"`

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

// IsProviderDisabled reports whether the user has explicitly turned this
// provider off. Providers with no config entry are treated as auto-enabled
// (so detected credentials work without manual setup).
func (c *Config) IsProviderDisabled(name string) bool {
	pc, ok := c.Providers[name]
	if !ok {
		return false
	}
	return !pc.Enabled
}

// IsProviderExplicitlyEnabled reports whether the user has explicitly opted a
// provider into polling in config.
func (c *Config) IsProviderExplicitlyEnabled(name string) bool {
	pc, ok := c.Providers[name]
	return ok && pc.Enabled
}

// configPath returns the canonical path to the config file: the
// platform-appropriate user config dir, plus clawmeter/config.yaml.
//
// On Linux this is $XDG_CONFIG_HOME/clawmeter/config.yaml (typically
// ~/.config/clawmeter/config.yaml); on macOS it is
// ~/Library/Application Support/clawmeter/config.yaml; on Windows it is
// %APPDATA%\clawmeter\config.yaml.
// ShouldCheckForUpdates reports whether automatic GitHub release checks are enabled.
func (c *Config) ShouldCheckForUpdates() bool {
	return c.Settings.CheckForUpdates == nil || *c.Settings.CheckForUpdates
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(dir, "clawmeter", "config.yaml"), nil
}

// legacyConfigPath returns the path clawmeter used before it adopted the
// platform-native config dir: ~/.config/clawmeter/config.yaml on every OS.
// On Linux this is identical to configPath(); on macOS/Windows it is not,
// and Load() migrates one-time.
func legacyConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "clawmeter", "config.yaml"), nil
}

// Load reads configuration from the config file.
// Returns default config if file doesn't exist.
//
// If the canonical path is missing but the legacy path
// (~/.config/clawmeter/config.yaml) is present and differs, Load migrates
// by copying the legacy file to the canonical path. The legacy file is
// left in place so a rollback to an older binary still works.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if migrated, mErr := migrateLegacyConfig(path); mErr != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: legacy config migration failed: %v\n", mErr)
		} else if migrated {
			fmt.Fprintf(os.Stderr, "clawmeter: migrated config to %s\n", path)
		}
	}

	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// migrateLegacyConfig copies the legacy config to the canonical path when
// the latter is absent and the two differ. Returns (true, nil) when a copy
// was performed, (false, nil) when no migration was needed, and
// (false, err) when migration was attempted but failed.
func migrateLegacyConfig(canonical string) (bool, error) {
	legacy, err := legacyConfigPath()
	if err != nil {
		return false, err
	}
	if legacy == canonical {
		return false, nil
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read legacy config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(canonical, data, 0o600); err != nil {
		return false, fmt.Errorf("write canonical config: %w", err)
	}
	return true, nil
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
