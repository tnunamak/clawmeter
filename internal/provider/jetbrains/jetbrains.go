// Package jetbrains implements the Provider interface for JetBrains AI Assistant.
package jetbrains

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// Provider implements the provider.Provider interface for JetBrains AI.
type Provider struct {
	cfg config.ProviderConfig
}

// New creates a new JetBrains provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func (p *Provider) Name() string         { return "jetbrains" }
func (p *Provider) DisplayName() string  { return "JetBrains" }
func (p *Provider) Description() string  { return "JetBrains AI Assistant (via local config)" }
func (p *Provider) DashboardURL() string { return "https://account.jetbrains.com/usage" }

func (p *Provider) IsConfigured() bool {
	_, err := p.findQuotaFile()
	return err == nil
}

func (p *Provider) FetchUsage(ctx context.Context) (*provider.UsageData, error) {
	path, err := p.findQuotaFile()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	xmlData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read quota file: %w", err)
	}

	quota, err := parseQuotaXML(xmlData)
	if err != nil {
		return nil, fmt.Errorf("parse quota: %w", err)
	}

	return p.transformQuota(quota), nil
}

// findQuotaFile finds the most recent JetBrains AI quota XML file.
func (p *Provider) findQuotaFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	configDir := filepath.Join(home, ".config", "JetBrains")
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return "", fmt.Errorf("read JetBrains config dir: %w", err)
	}

	// Collect all matching quota files, sorted by IDE version (newest first)
	type quotaFile struct {
		path    string
		modTime time.Time
	}
	var files []quotaFile

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		quotaPath := filepath.Join(configDir, entry.Name(), "options", "AIAssistantQuotaManager2.xml")
		info, err := os.Stat(quotaPath)
		if err != nil {
			continue
		}
		files = append(files, quotaFile{path: quotaPath, modTime: info.ModTime()})
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no JetBrains AI quota file found")
	}

	// Use the most recently modified file
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	return files[0].path, nil
}

// XML parsing types

type xmlApplication struct {
	XMLName   xml.Name       `xml:"application"`
	Component []xmlComponent `xml:"component"`
}

type xmlComponent struct {
	Name   string      `xml:"name,attr"`
	Option []xmlOption `xml:"option"`
}

type xmlOption struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type quotaData struct {
	MonthlyLimit int
	MonthlyUsed  int
	RefillDate   string // ISO date
}

func parseQuotaXML(data []byte) (*quotaData, error) {
	var app xmlApplication
	if err := xml.Unmarshal(data, &app); err != nil {
		return nil, err
	}

	quota := &quotaData{}

	for _, comp := range app.Component {
		for _, opt := range comp.Option {
			switch opt.Name {
			case "monthlyCreditsLimit":
				if v, err := strconv.Atoi(opt.Value); err == nil {
					quota.MonthlyLimit = v
				}
			case "monthlyCreditsUsed":
				if v, err := strconv.Atoi(opt.Value); err == nil {
					quota.MonthlyUsed = v
				}
			case "refillDate":
				quota.RefillDate = opt.Value
			}
		}
	}

	if quota.MonthlyLimit == 0 {
		return nil, fmt.Errorf("no quota data found in XML")
	}

	return quota, nil
}

func (p *Provider) transformQuota(quota *quotaData) *provider.UsageData {
	data := &provider.UsageData{
		Provider:  p.Name(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	usedPct := float64(quota.MonthlyUsed) / float64(quota.MonthlyLimit) * 100
	if usedPct > 100 {
		usedPct = 100
	}

	// Parse refill date
	resetsAt := time.Now().Add(30 * 24 * time.Hour) // default monthly
	if quota.RefillDate != "" {
		if t, err := time.Parse("2006-01-02", quota.RefillDate); err == nil {
			resetsAt = t
		} else if t, err := time.Parse(time.RFC3339, quota.RefillDate); err == nil {
			resetsAt = t
		}
	}

	data.Windows = append(data.Windows, provider.UsageWindow{
		Name:        "monthly",
		DisplayName: "Monthly Credits",
		Utilization: usedPct,
		ResetsAt:    resetsAt,
		Limit:       quota.MonthlyLimit,
		Used:        quota.MonthlyUsed,
	})

	return data
}

// Register registers the JetBrains provider with the registry.
func Register(registry *provider.Registry, cfg *config.Config) error {
	providerCfg, _ := cfg.GetProvider("jetbrains")
	return registry.Register(New(providerCfg))
}
