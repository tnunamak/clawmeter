package opencodefree

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchUsageListsZeroCostAndObservedFreeModels(t *testing.T) {
	dir := t.TempDir()
	db, catalogPath := filepath.Join(dir, "opencode.db"), filepath.Join(dir, "models.json")
	if err := os.WriteFile(db, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, []byte(`{"opencode":{"models":{"free":{"name":"Free candidate","cost":{"input":0,"output":0}},"paid-free":{"name":"paid","cost":{"input":1,"output":1}},"zero":{"cost":{"input":0,"output":0}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	seenArgs := []string(nil)
	rows := `[{"created":1793145600000,"model":"free-local-free","input":5,"output":2,"reasoning":null,"cache_read":0,"cache_write":null,"error_type":"","error_status":0},{"created":1793145601000,"model":"free-local-free","input":null,"output":null,"error_type":"provider.free-limit","error_status":429,"retry_after_ms":12000},{"created":1793145602000,"model":"paid-free","input":99}]`
	p := &Provider{dbPath: db, catalogPath: catalogPath, now: func() time.Time { return time.UnixMilli(1793145600000).UTC().Add(12 * time.Hour) }, run: func(_ context.Context, bin string, args ...string) ([]byte, error) {
		if bin != "sqlite3" {
			t.Fatalf("binary=%s", bin)
		}
		seenArgs = args
		return []byte(rows), nil
	}}
	day := time.UnixMilli(1793145600000).UTC().Format("2006-01-02")
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.LocalModels) != 3 {
		t.Fatalf("models=%+v", data.LocalModels)
	}
	if data.LocalModels[0].Model != "free" || data.LocalModels[0].AssistantMessages == nil || *data.LocalModels[0].AssistantMessages != 0 {
		t.Fatalf("unused catalog model=%+v", data.LocalModels[0])
	}
	observed := data.LocalModels[1]
	if observed.Model != "free-local-free" || observed.Day != day || observed.AssistantMessages == nil || *observed.AssistantMessages != 1 || observed.InputTokens == nil || *observed.InputTokens != 5 {
		t.Fatalf("observed=%+v", observed)
	}
	if observed.LimitHitAt.IsZero() || observed.RetryAt.Sub(observed.LimitHitAt) != 12*time.Second {
		t.Fatalf("limit metadata=%+v", observed)
	}
	if len(seenArgs) < 4 || seenArgs[0] != "-readonly" || seenArgs[1] != "-json" || strings.Contains(strings.Join(seenArgs, " "), "text") {
		t.Fatalf("sqlite args=%q", seenArgs)
	}
}

func TestReadMessagesRejectsBadRowsAndBoundsOutput(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	if err := os.WriteFile(db, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ name, body string }{
		{"outside day", `[{"created":1793145599999,"model":"x-free"}]`},
		{"negative", `[{"created":1793200000000,"model":"x-free","input":-1}]`},
		{"malformed", `[{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{dbPath: db, run: func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.body), nil }}
			if _, err := p.readMessages(context.Background(), now); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	p := &Provider{dbPath: db, run: func(context.Context, string, ...string) ([]byte, error) { return make([]byte, maxSQLiteOutput+1), nil }}
	if _, err := p.readMessages(context.Background(), now); err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Fatalf("oversized output error=%v", err)
	}
}

func TestCatalogRequiresExplicitZeroPriceAndRejectsMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "models.json")
	for _, body := range []string{`{}`, `{"opencode":{"models":null}}`, `[]`} {
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCatalog(p); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if !(modelInfo{Cost: &modelCost{Input: f(0), Output: f(0)}}).ZeroCost() {
		t.Fatal("zero price not recognized")
	}
	if (modelInfo{Cost: &modelCost{Input: f(0)}}).ZeroCost() {
		t.Fatal("missing output price treated as free")
	}
	if (modelInfo{Cost: &modelCost{Input: f(0), Output: f(0), CacheRead: f(2)}}).ZeroCost() {
		t.Fatal("nonzero cache price treated as free")
	}
}

func f(n float64) *float64 { return &n }

func TestSQLiteQueryIsReadOnlyAndModelScoped(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 unavailable")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	catalogPath := filepath.Join(dir, "catalog")
	sql := `CREATE TABLE session_message(type TEXT,time_created INTEGER,data TEXT);
 INSERT INTO session_message VALUES('assistant',1793145600000,'{"model":{"providerID":"opencode","id":"new-free"},"tokens":{"input":9,"output":4}}');
 INSERT INTO session_message VALUES('assistant',1793145600001,'{"model":{"providerID":"paid-provider","id":"new-free"},"tokens":{"input":999,"output":999}}');
 INSERT INTO session_message VALUES('assistant',1793145599999,'{"model":{"providerID":"opencode","id":"new-free"},"tokens":{"input":999,"output":999}}');`
	if out, err := exec.Command("sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	before, _ := os.ReadFile(db)
	if err := os.WriteFile(catalogPath, []byte(`{"opencode":{"models":{"new-free":{"cost":{"input":0,"output":0}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	p := &Provider{dbPath: db, catalogPath: catalogPath, run: runSQLite, now: func() time.Time { return time.UnixMilli(1793145600000).Add(time.Hour) }}
	data, err := p.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(data.LocalModels) != 1 || *data.LocalModels[0].AssistantMessages != 1 || *data.LocalModels[0].InputTokens != 9 {
		t.Fatalf("cross-provider/day contamination: %+v", data.LocalModels)
	}
	after, _ := os.ReadFile(db)
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("read changed database")
	}
}
