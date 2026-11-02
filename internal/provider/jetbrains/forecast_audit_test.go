package jetbrains

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/forecast"
)

func TestAuditM2UnknownMonthlyStart(t *testing.T) {
	data := (&Provider{}).transformQuota(&quotaData{MonthlyLimit: 100, MonthlyUsed: 9, RefillDate: "2026-03-01"})
	if got := forecast.WindowLength(data.Windows[0]); got != 0 {
		t.Fatalf("unknown billing start invented duration %v", got)
	}
}

func TestAuditM3QuotaFilePreservesObservationAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota.xml")
	writeQuotaFile(t, path, 100, 80)
	observed := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, observed, observed); err != nil {
		t.Fatal(err)
	}
	p := NewSource(config.ProviderConfig{}, config.SourceConfig{ID: "test", Credential: config.CredentialRef{Kind: "quota-file", Ref: path}})
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !data.FetchedAt.Equal(observed) {
		t.Fatalf("file observed at %v, got %v", observed, data.FetchedAt)
	}
	second, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !second.FetchedAt.Equal(data.FetchedAt) {
		t.Fatalf("unchanged file acquired a new observation: first=%v second=%v", data.FetchedAt, second.FetchedAt)
	}
	writeQuotaFile(t, path, 100, 90)
	updated := observed.Add(time.Hour)
	if err := os.Chtimes(path, updated, updated); err != nil {
		t.Fatal(err)
	}
	third, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !third.FetchedAt.Equal(updated) || !third.FetchedAt.After(second.FetchedAt) {
		t.Fatalf("updated file observation=%v, want %v after %v", third.FetchedAt, updated, second.FetchedAt)
	}
	if len(third.Windows) == 0 || third.Windows[0].Utilization != 90 {
		t.Fatalf("updated quota contents were not read: %+v", third.Windows)
	}
}
