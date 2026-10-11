package deepseek

import (
	"github.com/tnunamak/clawmeter/internal/config"
	"strings"
	"testing"
)

func TestP2NativeKeyRevision(t *testing.T) {
	p := New(config.ProviderConfig{APIKey: "synthetic-one"})
	first := p.SourceRevision()
	p.cfg.APIKey = "synthetic-two"
	second := p.SourceRevision()
	if first == "" || second == "" || first == second || strings.Contains(first, "synthetic") {
		t.Fatalf("native revisions do not isolate accounts: %q %q", first, second)
	}
	p.cfg.APIKey = ""
	t.Setenv("DEEPSEEK_API_KEY", "synthetic-env-one")
	first = p.SourceRevision()
	t.Setenv("DEEPSEEK_API_KEY", "synthetic-env-two")
	if first == p.SourceRevision() {
		t.Fatal("native environment key change reused revision")
	}
}
