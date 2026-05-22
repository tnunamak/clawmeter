package openai

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
)

func TestReadResponseReturnsSentinelOnEOF(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	_, err := readResponse(scanner)
	if !errors.Is(err, errNoResponse) {
		t.Fatalf("readResponse() error = %v, want errNoResponse", err)
	}
}

func TestFetchUsageRetriesTransientNoResponse(t *testing.T) {
	dir := t.TempDir()
	counterPath := filepath.Join(dir, "attempts")
	codexPath := filepath.Join(dir, "codex")
	resetPrimary := time.Now().Add(2 * time.Hour).Unix()
	resetSecondary := time.Now().Add(5 * 24 * time.Hour).Unix()
	script := `#!/bin/sh
counter_path="$1"
count=0
if [ -f "$counter_path" ]; then
  count="$(cat "$counter_path")"
fi
count=$((count + 1))
printf '%s' "$count" > "$counter_path"
if [ "$count" = "1" ]; then
  exit 0
fi
while IFS= read -r line; do
  case "$line" in
    *'"id":1'*) printf '{"id":1,"result":{}}\n' ;;
    *'"method":"initialized"'*) ;;
    *'"id":2'*) printf '{"id":2,"result":{"account":{"type":"chatgpt","email":"test@example.com","planType":"plus"},"requiresOpenaiAuth":false}}\n' ;;
    *'"id":3'*) printf '{"id":3,"result":{"rateLimits":{"primary":{"usedPercent":12,"resetsAt":__PRIMARY__},"secondary":{"usedPercent":34,"resetsAt":__SECONDARY__}}}}\n'; exit 0 ;;
  esac
done
`
	script = strings.ReplaceAll(script, "__PRIMARY__", itoa64(resetPrimary))
	script = strings.ReplaceAll(script, "__SECONDARY__", itoa64(resetSecondary))
	wrapper := "#!/bin/sh\nexec " + shellQuote(codexPath+".impl") + " " + shellQuote(counterPath) + "\n"
	if err := os.WriteFile(codexPath+".impl", []byte(script), 0755); err != nil {
		t.Fatalf("write fake codex impl: %v", err)
	}
	if err := os.WriteFile(codexPath, []byte(wrapper), 0755); err != nil {
		t.Fatalf("write fake codex wrapper: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := New(config.ProviderConfig{}).FetchUsage(context.Background())
	if err != nil {
		t.Fatalf("FetchUsage() error = %v", err)
	}
	if len(data.Windows) != 2 {
		t.Fatalf("FetchUsage() windows = %d, want 2", len(data.Windows))
	}
	if data.Windows[0].Utilization != 12 || data.Windows[1].Utilization != 34 {
		t.Fatalf("unexpected utilization: %+v", data.Windows)
	}
	attemptBytes, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("read attempt counter: %v", err)
	}
	if got := strings.TrimSpace(string(attemptBytes)); got != "2" {
		t.Fatalf("attempts = %s, want 2", got)
	}
}

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
