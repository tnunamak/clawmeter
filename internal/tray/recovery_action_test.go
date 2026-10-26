//go:build tray

package tray

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/view"
)

func TestStaleResetSnapshotOffersUsageAction(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "claude"}, false, nil, false)
	now := time.Now()
	data := &provider.UsageData{Provider: "claude", ResetCredits: &provider.UsageResetCredits{
		AvailableCount: 1, Snapshot: true, FetchedAt: now.Add(-time.Hour),
	}}
	trayRenderMu.Lock()
	updateResetCreditRow(menu, data, now)
	trayRenderMu.Unlock()
	if !menu.resetState.visible || !menu.resetState.enabled {
		t.Fatalf("stale snapshot has an instruction but no action: %+v", menu.resetState)
	}
	var opened []string
	openResetRow(menu, func(url string) { opened = append(opened, url) })
	if !reflect.DeepEqual(opened, []string{claudeweb.UsageURL}) || !strings.Contains(menu.resetState.title, "Open Claude Usage…") {
		t.Fatalf("reset action = %q, row %q", opened, menu.resetState.title)
	}
	trayRenderMu.Lock()
	data.ResetCredits.FetchedAt = now
	updateResetCreditRow(menu, data, now)
	trayRenderMu.Unlock()
	openResetRow(menu, func(url string) { opened = append(opened, url) })
	if len(opened) != 1 || menu.resetState.enabled || menu.resetURL != "" {
		t.Fatal("fresh snapshot retained the stale recovery action")
	}
}

func TestRecoveryCommandsKeepTheNamedSourceAndQuoteItsPath(t *testing.T) {
	current := config.DefaultConfig()
	path := "/profiles/Tim's Work; echo no"
	current.Providers["claude"] = config.ProviderConfig{Sources: []config.SourceConfig{{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: path}}}}
	p := sourceMenuTestProvider{name: "claude", id: "work"}
	if got := sourceLoginCommands(p, current, "linux"); !reflect.DeepEqual(got, []string{"CLAUDE_CONFIG_DIR='/profiles/Tim'\"'\"'s Work; echo no' claude"}) {
		t.Fatalf("Unix source command = %q", got)
	}
	if got := sourceLoginCommands(p, current, "windows"); !reflect.DeepEqual(got, []string{"$env:CLAUDE_CONFIG_DIR = '/profiles/Tim''s Work; echo no'; claude"}) {
		t.Fatalf("PowerShell source command = %q", got)
	}
	current.Providers["openai"] = config.ProviderConfig{Sources: []config.SourceConfig{{ID: "work", Credential: config.CredentialRef{Kind: "codex-home", Ref: "/profiles/codex work"}}}}
	got := sourceLoginCommands(sourceMenuTestProvider{name: "openai", id: "work"}, current, "linux")
	want := []string{"CODEX_HOME='/profiles/codex work' codex logout", "CODEX_HOME='/profiles/codex work' codex login"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Codex source command = %q", got)
	}
	if got := sourceLoginCommands(sourceMenuTestProvider{name: "claude", id: "unknown"}, current, "linux"); got != nil {
		t.Fatalf("unknown source fell back to default: %q", got)
	}
	current.Providers["claude"] = config.ProviderConfig{OAuthToken: "configured-secret"}
	if got := sourceLoginCommands(sourceMenuTestProvider{name: "claude"}, current, "linux"); got != nil {
		t.Fatalf("config token treated as CLI login: %q", got)
	}
}

type boundRecoveryProvider struct {
	sourceMenuTestProvider
	route string
}

func (p boundRecoveryProvider) CredentialRoute() string { return p.route }

func TestRecoveryCommandsUseTheProvidersBoundRoute(t *testing.T) {
	p := boundRecoveryProvider{sourceMenuTestProvider: sourceMenuTestProvider{name: "claude"}, route: provider.CredentialRoute("native", "/bound/profile")}
	if got := sourceLoginCommands(p, config.DefaultConfig(), "linux"); !reflect.DeepEqual(got, []string{"CLAUDE_CONFIG_DIR='/bound/profile' claude"}) {
		t.Fatalf("bound command = %q", got)
	}
	p.id = "work"
	p.route = provider.CredentialRoute("config-dir", "/old/profile")
	current := config.DefaultConfig()
	current.Providers["claude"] = config.ProviderConfig{Sources: []config.SourceConfig{{ID: "work", Credential: config.CredentialRef{Kind: "config-dir", Ref: "/new/profile"}}}}
	if got := sourceLoginCommands(p, current, "linux"); got != nil {
		t.Fatalf("repointed source generated commands for another login: %q", got)
	}
}

func TestNativeSignInRecoveryDoesNotPretendBrowserLoginRenewsCLI(t *testing.T) {
	for _, family := range []string{"claude", "openai"} {
		action := providerRecovery(sourceMenuTestProvider{name: family}, "token expired", config.DefaultConfig(), "linux")
		if action.url != "" || action.help.Title == "" || len(action.help.Commands) == 0 || !strings.Contains(action.help.Description, "browser sign-in alone does not renew") {
			t.Fatalf("native recovery = %+v", action)
		}
	}
}

func TestRecoveryClicksRejectHiddenOrReplacedSources(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "claude", revision: "new-login"}, false, nil, false)
	now := time.Now()
	trayRenderMu.Lock()
	menu.sourceRevision = "old-login"
	updateResetCreditRow(menu, &provider.UsageData{Provider: "claude", ResetCredits: &provider.UsageResetCredits{AvailableCount: 1, Snapshot: true, FetchedAt: now.Add(-time.Hour)}}, now)
	menu.announceURL = "https://example.com/post"
	menu.announceState.visible = true
	menu.balanceActions[0] = "open your browser to update"
	menu.balanceStates[0].visible, menu.balanceStates[0].enabled = true, true
	menu.headerState.visible, menu.headerState.enabled = true, true
	menu.headerRecovery = recoveryAction{label: "Open usage…", url: claudeweb.UsageURL}
	trayRenderMu.Unlock()
	opens := 0
	open := func(string) { opens++ }
	openResetRow(menu, open)
	openAnnouncement(menu, open)
	openBalanceRow(menu, 0, open)
	openProviderHeader(menu, open, func(string) { t.Fatal("stale source connected") })
	if opens != 0 {
		t.Fatalf("replaced source opened %d pages", opens)
	}
	trayRenderMu.Lock()
	menu.sourceRevision = "new-login"
	hideProviderMenu(menu)
	trayRenderMu.Unlock()
	openResetRow(menu, open)
	openAnnouncement(menu, open)
	openBalanceRow(menu, 0, open)
	openProviderHeader(menu, open, func(string) { t.Fatal("hidden source connected") })
	if opens != 0 {
		t.Fatalf("hidden source opened %d pages", opens)
	}
}

func TestMissingExtensionActionOpensSetupInsteadOfAnUnhelpfulConsoleVisit(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: provider.ClaudeAPISourceName}, false, nil, false)
	now := time.Now()
	pool := provider.UsageCreditPool{Name: "Org", Currency: "USD", Balance: 100, ObservationSource: "extension", ObservedAt: now.Add(-time.Hour)}
	// The ordinary fixture prepares the extension; redraw as an unprepared source.
	drawBalanceRows(t, menu, time.Time{}, pool)
	trayRenderMu.Lock()
	menu.extensionReady = false
	acct := &view.Account{Provider: provider.ClaudeAPISourceName, Pools: []provider.UsageCreditPool{pool}}
	showBalanceRows(menu, acct, nil, now)
	trayRenderMu.Unlock()
	setups := 0
	openBalanceRowWithSetup(menu, 0, func(string) { t.Fatal("opened Console without extension setup") }, func() { setups++ })
	if setups != 1 || menu.balanceActions[0] != setupExtensionAction {
		t.Fatalf("setups = %d, action %q", setups, menu.balanceActions[0])
	}
}

func TestManualRefreshRequestsCoalesceWhileWaiting(t *testing.T) {
	var action manualRefreshAction
	started, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	if !action.request(func() { close(started); <-finish; close(done) }) {
		t.Fatal("first request dropped")
	}
	<-started
	if action.request(func() { t.Error("duplicate refresh ran") }) {
		t.Error("duplicate request queued")
	}
	close(finish)
	<-done
}

func TestStatusRowOpensTheIncidentPageOrRequestsFreshUsage(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "claude"}, false, nil, false)
	refreshes := 0
	trayRenderMu.Lock()
	menu.statusState.visible, menu.statusState.enabled = true, true
	menu.statusURL = "https://status.claude.com"
	menu.refreshUsage = func() { refreshes++ }
	trayRenderMu.Unlock()
	var opened []string
	open := func(url string) { opened = append(opened, url) }
	openStatusRow(menu, open)
	if !reflect.DeepEqual(opened, []string{"https://status.claude.com"}) || refreshes != 0 {
		t.Fatalf("incident action = %q, refreshes %d", opened, refreshes)
	}
	trayRenderMu.Lock()
	menu.statusURL = ""
	trayRenderMu.Unlock()
	openStatusRow(menu, open)
	if refreshes != 1 || len(opened) != 1 {
		t.Fatalf("stale action = %q, refreshes %d", opened, refreshes)
	}
	trayRenderMu.Lock()
	menu.statusState.visible = false
	trayRenderMu.Unlock()
	openStatusRow(menu, open)
	if refreshes != 1 || len(opened) != 1 {
		t.Fatal("hidden status retained an action")
	}
}

func TestRecoveryTargetCreatesTheHelpDirectoryOnFirstUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("LOCALAPPDATA", home)
	target, err := prepareRecoveryTarget(providerSetupAction())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "file" {
		t.Fatalf("target = %q, %v", target, err)
	}
	dir, _ := cache.Path("recovery")
	if _, err := os.Stat(filepath.Join(dir, "recovery-help.html")); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderConnectionCarriesTheFullSourceKey(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: tokenPlanProviderName, id: "work"}, false, make(chan string), true)
	trayRenderMu.Lock()
	setProviderConnectionItemLocked(menu, "Reconnect quota access", true)
	trayRenderMu.Unlock()
	var key string
	openProviderHeader(menu, func(string) { t.Fatal("connection opened an unrelated dashboard") }, func(source string) { key = source })
	if key != tokenPlanProviderName+":work" {
		t.Fatalf("connection key = %q", key)
	}
}

func TestHealthyHeaderClearsTheProblemRecovery(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "claude"}, false, nil, false)
	trayRenderMu.Lock()
	showProviderProblem(menu, "token expired")
	if menu.headerRecovery.label != "Show sign-in steps…" || !menu.headerState.enabled {
		trayRenderMu.Unlock()
		t.Fatalf("missing sign-in action: %+v", menu.headerState)
	}
	showProviderHeader(menu)
	cleared := menu.headerRecovery.label == "" && !menu.headerState.enabled
	trayRenderMu.Unlock()
	if !cleared {
		t.Fatal("healthy non-dashboard header retained a sign-in action")
	}
}

func TestPlanAccessProblemOpensTheNamedPlatformInsteadOfUsage(t *testing.T) {
	defer systray.ResetMenu()
	menu := createProviderMenuItems(sourceMenuTestProvider{name: "openai"}, false, nil, false)
	trayRenderMu.Lock()
	showProviderProblem(menu, "authentication required to read rate limits")
	trayRenderMu.Unlock()
	var target string
	openProviderHeader(menu, func(url string) { target = url }, func(string) { t.Fatal("plan problem started a native login") })
	if target != "https://platform.openai.com/" {
		t.Fatalf("plan target = %q", target)
	}
}
