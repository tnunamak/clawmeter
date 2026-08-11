// Package jetbrains implements the Provider interface for JetBrains AI Assistant.
package jetbrains

import (
	"context"
	"crypto/sha256"
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
	cfg            config.ProviderConfig
	sourceID       string
	sourceLabel    string
	quotaFile      string
	explicitSource bool
	enrolledSource bool
}

// New creates a new JetBrains provider.
func New(cfg config.ProviderConfig) *Provider {
	return &Provider{
		cfg: cfg,
	}
}

func NewNativeSource(cfg config.ProviderConfig, source config.SourceConfig) *Provider {
	if source.Credential.Kind == "" {
		source.Credential.Kind = "native"
	}
	p, _ := newSource(cfg, source)
	return p
}

func NewSource(cfg config.ProviderConfig, source config.SourceConfig) *Provider {
	p, _ := newSource(cfg, source)
	return p
}

func newSource(cfg config.ProviderConfig, source config.SourceConfig) (*Provider, error) {
	p, err := (sourceCapability{}).NewSource(cfg, source)
	if err != nil {
		return nil, err
	}
	return p.(*Provider), nil
}

type sourceCapability struct{}

func (*Provider) SourceKinds() []provider.SourceKind { return (sourceCapability{}).SourceKinds() }
func (*Provider) DefaultSource() (config.SourceConfig, bool) {
	return (sourceCapability{}).DefaultSource()
}
func (*Provider) ValidateSource(source config.SourceConfig) error {
	return (sourceCapability{}).ValidateSource(source)
}
func (*Provider) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	return (sourceCapability{}).NewSource(cfg, source)
}

func (sourceCapability) SourceKinds() []provider.SourceKind {
	return []provider.SourceKind{
		{Kind: "native", Summary: "Provider's legacy/default quota-file discovery"},
		{Kind: "quota-file", Summary: "Exact JetBrains AIAssistantQuotaManager2.xml observation; absolute path required", RefUsage: "/absolute/path/AIAssistantQuotaManager2.xml", RefRequired: true, RefIsPath: true},
	}
}

func (sourceCapability) DefaultSource() (config.SourceConfig, bool) {
	return config.SourceConfig{ID: "default", Label: "Default", Credential: config.CredentialRef{Kind: "native"}}, true
}

func (sourceCapability) ValidateSource(source config.SourceConfig) error {
	switch source.Credential.Kind {
	case "native":
		if source.ID != "default" || source.Credential.Ref != "" {
			return fmt.Errorf("provider %q source %q cannot use native credentials", "jetbrains", source.ID)
		}
	case "quota-file":
		if source.Credential.Ref == "" {
			return fmt.Errorf("provider %q source %q has empty quota file", "jetbrains", source.ID)
		}
		if !filepath.IsAbs(source.Credential.Ref) {
			return fmt.Errorf("provider %q source %q has relative quota file", "jetbrains", source.ID)
		}
	default:
		return fmt.Errorf("provider %q source %q has unsupported credential kind %q", "jetbrains", source.ID, source.Credential.Kind)
	}
	return nil
}

func (sourceCapability) NewSource(cfg config.ProviderConfig, source config.SourceConfig) (provider.Provider, error) {
	if err := (sourceCapability{}).ValidateSource(source); err != nil {
		return nil, err
	}
	p := &Provider{cfg: cfg, sourceID: source.ID, sourceLabel: source.Label, enrolledSource: true}
	if source.Credential.Kind == "quota-file" {
		p.quotaFile = source.Credential.Ref
		p.explicitSource = true
	}
	return p, nil
}

func (p *Provider) SourceID() string {
	if p.sourceID == "" {
		return "default"
	}
	return p.sourceID
}
func (p *Provider) SourceLabel() string    { return p.sourceLabel }
func (p *Provider) IsEnrolledSource() bool { return p.enrolledSource }
func (p *Provider) SourceRevision() string {
	if !p.explicitSource {
		return ""
	}
	return sourceRevision(p.quotaFile)
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
	if p.explicitSource {
		if _, err := os.Stat(p.quotaFile); err != nil {
			return "", fmt.Errorf("quota file: %w", err)
		}
		return p.quotaFile, nil
	}

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
	HasLimit     bool
	HasUsed      bool
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
					quota.HasLimit = true
				}
			case "monthlyCreditsUsed":
				if v, err := strconv.Atoi(opt.Value); err == nil {
					quota.MonthlyUsed = v
					quota.HasUsed = true
				}
			case "refillDate":
				quota.RefillDate = opt.Value
			}
		}
	}

	if !quota.HasLimit || quota.MonthlyLimit <= 0 || !quota.HasUsed || quota.MonthlyUsed < 0 {
		return nil, fmt.Errorf("no quota data found in XML")
	}

	return quota, nil
}

func (p *Provider) transformQuota(quota *quotaData) *provider.UsageData {
	// A quota-file source is one exact XML observation. Separate files are
	// separate observations, not proof that JetBrains exposes separate account pools.
	data := &provider.UsageData{
		Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
		FetchedAt: time.Now(),
		Windows:   make([]provider.UsageWindow, 0),
	}

	usedPct := float64(quota.MonthlyUsed) / float64(quota.MonthlyLimit) * 100
	if usedPct > 100 {
		usedPct = 100
	}

	// Parse refill date
	var resetsAt time.Time
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
	return provider.RegisterConfigured(registry, providerCfg, New(providerCfg))
}

func sourceRevision(path string) string {
	canonical, err := filepath.Abs(path)
	if err != nil {
		canonical = path
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(canonical+"\x00unavailable")))
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%o", canonical, info.Size(), info.ModTime().UnixNano(), info.Mode().Perm()))))
}

var _ provider.SourceCapability = (*Provider)(nil)
