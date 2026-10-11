package gemini

import (
	"github.com/tnunamak/clawmeter/internal/config"
	"testing"
	"time"
)

func TestP2QuotaBucketReset(t *testing.T) {
	one, half := 1.0, 0.5
	reset := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	p := New(config.ProviderConfig{})
	got := p.transformQuota(&quotaResponse{Buckets: []quotaBucket{{ModelID: "pro", RemainingFraction: &one, ResetTime: reset.Format(time.RFC3339)}}})
	if len(got.Windows) != 1 || !got.Windows[0].ResetsAt.Equal(reset) {
		t.Fatalf("zero-use bucket lost reset: %#v", got.Windows)
	}
	got = p.transformQuota(&quotaResponse{Buckets: []quotaBucket{{ModelID: "pro", RemainingFraction: &half, ResetTime: reset.Format(time.RFC3339)}, {ModelID: "pro", RemainingFraction: new(float64), ResetTime: "invalid"}}})
	if len(got.Windows) != 1 || !got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("worst bucket inherited unrelated reset: %#v", got.Windows)
	}
}
