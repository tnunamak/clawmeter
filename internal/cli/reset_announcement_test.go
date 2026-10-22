package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/resetfeed"
)

// codexStub is a configured Codex (openai) provider for the attach gate.
type codexStub struct{}

func (codexStub) Name() string                                            { return "openai" }
func (codexStub) DisplayName() string                                     { return "Codex" }
func (codexStub) Description() string                                     { return "" }
func (codexStub) DashboardURL() string                                    { return "" }
func (codexStub) IsConfigured() bool                                      { return true }
func (codexStub) FetchUsage(context.Context) (*provider.UsageData, error) { return nil, nil }

// blockedCodex is a fresh Codex reading whose 7d window is out now, with its
// reset three days away.
func blockedCodex(now time.Time) *provider.UsageData {
	return &provider.UsageData{
		Provider: "openai", SourceID: "default", FetchedAt: now,
		Windows:      []provider.UsageWindow{{Name: "7d", Utilization: 100, ResetsAt: now.Add(72 * time.Hour), Length: 7 * 24 * time.Hour}},
		ResetCredits: &provider.UsageResetCredits{AvailableCount: 2, EarliestExpiresAt: now.Add(9 * 24 * time.Hour), FetchedAt: now},
	}
}

// announcedFor attaches an upcoming scheduled reset to data as attach would.
func announcedFor(t *testing.T, data *provider.UsageData, now time.Time) *provider.UsageData {
	t.Helper()
	reading := resetfeed.Reading{
		CheckedAt: now,
		Announcement: &resetfeed.Announcement{
			ID:           "evt_1",
			ScheduledFor: now.Add(2 * time.Hour),
			AnnouncedAt:  now.Add(-time.Hour),
			SourceURL:    "https://x.com/example/status/1",
		},
	}
	out := resetfeed.Announce(data, reading, now)
	if out.AnnouncedReset == nil {
		t.Fatal("fixture: expected a visible announcement")
	}
	return out
}

func codexFormatter(data *provider.UsageData) ProviderFormatter {
	return ProviderFormatter{Name: "openai", Family: "openai", SourceID: "default", Display: "Codex", Data: data}
}

func TestAnnouncementPlainLineFollowsResetCredits(t *testing.T) {
	now := time.Now()
	data := announcedFor(t, blockedCodex(now), now)
	pf := codexFormatter(data)

	line := pf.FormatPlain()
	credits := strings.Index(line, "Resets: 2")
	announced := strings.Index(line, data.AnnouncedReset.Summary(time.Now()))
	if credits < 0 || announced < 0 {
		t.Fatalf("plain line lacks reset credits or announcement: %q", line)
	}
	if announced < credits {
		t.Fatalf("announcement must follow the reset-credit text: %q", line)
	}
	if !strings.HasPrefix(data.AnnouncedReset.Summary(now), "Reset announced for ~") || !strings.HasSuffix(line, "· codex-resets.com") {
		t.Fatalf("announcement copy = %q", line)
	}
}

func TestAnnouncementColorLineFollowsResetCredits(t *testing.T) {
	now := time.Now()
	data := announcedFor(t, blockedCodex(now), now)
	pf := codexFormatter(data)

	lines := pf.FormatColorAligned(newTableLayout())
	joined := strings.Join(lines, "\n")
	want := data.AnnouncedReset.Summary(time.Now())
	if !strings.Contains(joined, want) {
		t.Fatalf("color output lacks %q:\n%s", want, joined)
	}
	if strings.Index(joined, "Resets:") > strings.Index(joined, want) {
		t.Fatalf("announcement must follow the reset-credit row:\n%s", joined)
	}
}

func TestAnnouncementAgentFieldInQuotaSummary(t *testing.T) {
	now := time.Now()
	data := announcedFor(t, blockedCodex(now), now)
	output := &MultiProviderOutput{Providers: []ProviderFormatter{codexFormatter(data)}}

	line := output.AgentSummary()
	want := "announced_reset=[~" + data.AnnouncedReset.ScheduledFor.Local().Format(time.RFC3339) + " unofficial source=codex-resets.com windows=7d]"
	if !strings.Contains(line, "Codex 7d(") || !strings.Contains(line, want) {
		t.Fatalf("agent line lacks %q:\n%s", want, line)
	}
	if strings.Count(line, "announced_reset=") != 1 {
		t.Fatalf("announcement must appear once: %s", line)
	}
}

func TestAnnouncementJSONField(t *testing.T) {
	now := time.Now()
	data := announcedFor(t, blockedCodex(now), now)
	output := &MultiProviderOutput{Providers: []ProviderFormatter{codexFormatter(data)}}

	raw := captureStdout(t, func() { output.PrintJSON(nil) })
	var parsed struct {
		Providers map[string]struct {
			Usage struct {
				AnnouncedReset map[string]any `json:"announced_reset"`
			} `json:"usage"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("json: %v\n%s", err, raw)
	}
	got := parsed.Providers["openai"].Usage.AnnouncedReset
	if got == nil {
		t.Fatalf("usage.announced_reset missing:\n%s", raw)
	}
	if got["kind"] != "third_party_announcement" || got["official"] != false || got["feed"] != "codex-resets.com" {
		t.Fatalf("announced_reset = %v", got)
	}
	if got["approximate"] != true || got["source_url"] != "https://x.com/example/status/1" {
		t.Fatalf("announced_reset = %v", got)
	}
	if !reflect.DeepEqual(got["windows"], []any{"7d"}) {
		t.Fatalf("windows = %v", got["windows"])
	}
}

func TestAnnouncementNeverChangesQuotaOrExitCode(t *testing.T) {
	now := time.Now()
	plain := codexFormatter(blockedCodex(now))
	announced := codexFormatter(announcedFor(t, blockedCodex(now), now))

	withOutput := &MultiProviderOutput{Providers: []ProviderFormatter{announced}}
	withoutOutput := &MultiProviderOutput{Providers: []ProviderFormatter{plain}}

	if statusCheckExitCode(withOutput) != statusCheckExitCode(withoutOutput) {
		t.Fatal("announcement changed the exit code")
	}
	summary := announced.Data.AnnouncedReset.Summary(time.Now())
	if got, want := strings.Replace(announced.FormatPlain(), "  "+summary, "", 1), plain.FormatPlain(); got != want {
		t.Fatalf("announcement changed the plain quota line:\n got %q\nwant %q", got, want)
	}
	if got, want := strings.Replace(withOutput.AgentSummary(), ","+announced.Data.AnnouncedReset.AgentField(), "", 1), withoutOutput.AgentSummary(); got != want {
		t.Fatalf("announcement changed the agent quota summary:\n got %q\nwant %q", got, want)
	}
	if !reflect.DeepEqual(announced.Data.Windows, plain.Data.Windows) {
		t.Fatal("announcement changed windows")
	}
}

// stubFeed points the CLI at a local server and counts its requests.
func stubFeed(t *testing.T, status int, body string) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	previous := resetFeedClient
	resetFeedClient = func() (*resetfeed.Client, error) {
		return &resetfeed.Client{BaseURL: srv.URL, HTTP: srv.Client(), Path: filepath.Join(t.TempDir(), resetfeed.CacheFileName)}, nil
	}
	t.Cleanup(func() { resetFeedClient = previous })
	return &hits
}

func codexRegistry(t *testing.T) *provider.Registry {
	t.Helper()
	registry := provider.NewRegistry()
	if err := registry.Register(codexStub{}); err != nil {
		t.Fatal(err)
	}
	return registry
}

// upcomingBody is a feed status with a regular reset scheduled two hours from
// now, which is before the fixture window's reset three days away.
func upcomingBody() string {
	scheduled := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	return `{"data":{"scheduled_reset":{"id":"evt_1","status":"scheduled","reset_type":"regular","announced_at":"` +
		time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) + `","scheduled_for":"` + scheduled +
		`","source":{"url":"https://x.com/example/status/1"}}}}`
}

func TestSettingOffMakesNoRequest(t *testing.T) {
	hits := stubFeed(t, http.StatusOK, upcomingBody())
	cfg := config.DefaultConfig()
	output := &MultiProviderOutput{Providers: []ProviderFormatter{codexFormatter(blockedCodex(time.Now()))}}

	attachResetAnnouncements(output, codexRegistry(t), cfg, time.Now())
	if hits.Load() != 0 {
		t.Fatalf("setting off made %d requests, want 0", hits.Load())
	}
	if output.Providers[0].Data.AnnouncedReset != nil {
		t.Fatal("setting off must not attach")
	}
}

func TestNoCodexSourceMakesNoRequest(t *testing.T) {
	hits := stubFeed(t, http.StatusOK, upcomingBody())
	cfg := config.DefaultConfig()
	cfg.Settings.CodexResetAnnouncements = true
	output := &MultiProviderOutput{}

	attachResetAnnouncements(output, provider.NewRegistry(), cfg, time.Now())
	if hits.Load() != 0 {
		t.Fatalf("no Codex source made %d requests, want 0", hits.Load())
	}
}

func TestSettingOnAttachesOnceAndFailureChangesNothing(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Settings.CodexResetAnnouncements = true
	now := time.Now()

	hits := stubFeed(t, http.StatusOK, upcomingBody())
	output := &MultiProviderOutput{Providers: []ProviderFormatter{codexFormatter(blockedCodex(now))}}
	attachResetAnnouncements(output, codexRegistry(t), cfg, now)
	if hits.Load() != 1 {
		t.Fatalf("requests = %d, want 1", hits.Load())
	}
	if output.Providers[0].Data.AnnouncedReset == nil {
		t.Fatal("want the announcement attached")
	}

	stubFeed(t, http.StatusInternalServerError, "")
	failing := &MultiProviderOutput{Providers: []ProviderFormatter{codexFormatter(blockedCodex(now))}}
	attachResetAnnouncements(failing, codexRegistry(t), cfg, now)
	if failing.Providers[0].Data.AnnouncedReset != nil {
		t.Fatal("failed feed with no last good reading must show nothing")
	}
	if !reflect.DeepEqual(failing.Providers[0].Data.Windows, blockedCodex(now).Windows) {
		t.Fatal("feed failure changed quota windows")
	}
}

func TestFeedErrorIsNotAUserError(t *testing.T) {
	stubFeed(t, http.StatusTooManyRequests, "")
	cfg := config.DefaultConfig()
	cfg.Settings.CodexResetAnnouncements = true
	output := &MultiProviderOutput{Providers: []ProviderFormatter{codexFormatter(blockedCodex(time.Now()))}}
	attachResetAnnouncements(output, codexRegistry(t), cfg, time.Now())
	if output.Providers[0].Data.Error != "" {
		t.Fatalf("feed error leaked into provider data: %q", output.Providers[0].Data.Error)
	}
}
