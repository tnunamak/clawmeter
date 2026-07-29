package provider_test

import (
	"testing"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/alibaba"
)

func TestRegistryGetConfigured_SelectsAlibabaCodingPlan(t *testing.T) {
	registry := provider.NewRegistry()
	if err := registry.Register(alibaba.New(config.ProviderConfig{APIKey: "sk-sp-coding-plan-key"})); err != nil {
		t.Fatal(err)
	}

	configured := registry.GetConfigured()
	if len(configured) != 1 || configured[0].Name() != "alibaba" {
		t.Fatalf("GetConfigured() = %v, want [alibaba]", providerNames(configured))
	}
}

func providerNames(providers []provider.Provider) []string {
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	return names
}
