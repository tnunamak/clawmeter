//go:build tray

package tray

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/tnunamak/clawmeter/internal/cache"
	"github.com/tnunamak/clawmeter/internal/claudeweb"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/format"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/update"
)

// recoveryAction describes the next step, rather than interpreting arbitrary
// error text as a URL or command. Opening help never executes its commands.
type recoveryAction struct {
	label          string
	problem        string
	url            string
	help           recoveryHelpPage
	setupExtension bool
}

func providerRecovery(p provider.Provider, problem string, current *config.Config, goos string) recoveryAction {
	family := provider.FamilyName(p)
	account := providerMenuName(p, true)
	lower := strings.ToLower(problem)
	if family == provider.ClaudeAPISourceName {
		return recoveryAction{label: "Open Claude Console…", url: claudeweb.ConsoleURL}
	}
	if family == "openai" && strings.Contains(lower, "authentication required to read rate limits") {
		return recoveryAction{label: "Open OpenAI Platform…", problem: "Quota access needs attention", url: "https://platform.openai.com/"}
	}
	auth := strings.Contains(lower, "expired") || strings.Contains(lower, "sign-in") || strings.Contains(lower, "unauthorized") || provider.ClassifyError(errors.New(problem)) == "auth"
	if auth && (family == "claude" || family == "openai") {
		page := recoveryHelpPage{
			Title: "Sign in to " + p.DisplayName(), Account: account,
			Description: "Run these commands in a terminal for this source, then choose Refresh Now in Clawmeter. A browser sign-in alone does not renew this tool's login.",
		}
		page.Commands = sourceLoginCommands(p, current, goos)
		if len(page.Commands) == 0 {
			page.Description = "This source uses a credential route that Clawmeter cannot safely turn into a sign-in command. Check its configured route before renewing the login."
			page.Commands = []string{"clawmeter providers source list " + family}
		}
		return recoveryAction{label: "Show sign-in steps…", problem: "Sign-in expired", help: page}
	}
	page := recoveryHelpPage{
		Title: "Check " + p.DisplayName() + " connection", Account: account,
		Description: format.HumanizeError(problem) + ". Check the provider's setup in a terminal, then choose Refresh Now.",
		Commands:    []string{"clawmeter providers diagnose " + family + " --pretty"},
	}
	if target := p.DashboardURL(); target != "" {
		page.Links = []recoveryHelpLink{{Label: "Open " + p.DisplayName() + " dashboard", URL: target}}
	}
	if family == tokenPlanProviderName {
		page.Description = "Complete these steps in a terminal to reconnect quota access, then choose Refresh Now in Clawmeter."
		switch {
		case strings.Contains(lower, "missing") || strings.Contains(lower, "not installed"):
			page.Commands = []string{"npm install --global bailian-cli", "bl auth login --console"}
			page.Description = "Install Node.js 22.12 or later before running these commands. Use the final command to reconnect quota access, then choose Refresh Now in Clawmeter."
			page.Links = append(page.Links, recoveryHelpLink{Label: "Install Node.js", URL: "https://nodejs.org/"})
		case strings.Contains(lower, "session") || strings.Contains(lower, "authorization"):
			page.Commands = []string{"bl auth login --console"}
		default:
			page.Commands = nil
		}
		page.Commands = append(page.Commands, "clawmeter providers connect token-plan --force")
	}
	return recoveryAction{label: "Show connection steps…", help: page}
}

func sourceLoginCommands(p provider.Provider, current *config.Config, goos string) []string {
	family := provider.FamilyName(p)
	var source config.SourceConfig
	var count int
	if current != nil {
		pc := current.Providers[family]
		// A config-supplied token is not renewed by signing in to a CLI.
		if pc.OAuthToken != "" || pc.APIKey != "" {
			return nil
		}
		count = len(pc.Sources)
		for _, candidate := range pc.Sources {
			if candidate.ID == provider.SourceID(p) {
				source = candidate
				break
			}
		}
	}
	variable, dir := "", ""
	switch family {
	case "claude":
		variable = "CLAUDE_CONFIG_DIR"
		if source.Credential.Kind == "config-dir" {
			dir = source.Credential.Ref
		} else if source.Credential.Kind != "" && source.Credential.Kind != "native" {
			return nil
		} else if provider.SourceID(p) != "default" {
			return nil
		} else if count > 1 {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil
			}
			dir = filepath.Join(home, ".claude")
		}
	case "openai":
		variable = "CODEX_HOME"
		if source.Credential.Kind == "codex-home" {
			dir = source.Credential.Ref
		} else if (source.Credential.Kind != "" && source.Credential.Kind != "native") || provider.SourceID(p) != "default" {
			return nil
		}
	default:
		return nil
	}
	// Prefer the running provider's bound route over ambient shell settings.
	// A config edit that repoints an existing source must not produce commands
	// for a different login than the source still represented by this menu.
	if route, ok := p.(provider.CredentialRouteCapability); ok {
		kind, ref, valid := strings.Cut(route.CredentialRoute(), "\x00")
		if !valid || ref == "" || (kind != "native" && kind != "native-pinned" && kind != "config-dir" && kind != "codex-home") {
			return nil
		}
		if dir != "" && source.Credential.Ref != "" && provider.CanonicalPath(dir) != provider.CanonicalPath(ref) {
			return nil
		}
		dir = ref
	}
	commands := []string{"claude"}
	if family == "openai" {
		commands = []string{"codex logout", "codex login"}
	}
	if dir != "" {
		for i, command := range commands {
			if goos == "windows" {
				commands[i] = "$env:" + variable + " = '" + strings.ReplaceAll(dir, "'", "''") + "'; " + command
			} else {
				commands[i] = variable + "='" + strings.ReplaceAll(dir, "'", "'\"'\"'") + "' " + command
			}
		}
	}
	return commands
}

func openRecovery(action recoveryAction, open func(string)) {
	if action.setupExtension {
		openExtensionSetup()
		return
	}
	target, err := prepareRecoveryTarget(action)
	if err != nil {
		notify("Clawmeter", "Could not open recovery steps: "+err.Error(), "normal")
		return
	}
	if target != "" {
		open(target)
	}
}

func prepareRecoveryTarget(action recoveryAction) (string, error) {
	if action.url != "" {
		return action.url, nil
	}
	if action.help.Title == "" {
		return "", nil
	}
	path, err := cache.Path("recovery")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", err
	}
	return writeRecoveryHelp(path, action.help)
}

func providerSetupAction() recoveryAction {
	return recoveryAction{help: recoveryHelpPage{
		Title: "Set up providers", Description: "Open a terminal to list detected providers and their connection steps. Clawmeter reads existing tool logins; opening a provider website does not connect its CLI.",
		Commands: []string{"clawmeter providers", "clawmeter providers help"},
		Links:    []recoveryHelpLink{{Label: "Clawmeter setup guide", URL: "https://github.com/tnunamak/clawmeter#install"}},
	}}
}

func configRecoveryAction() recoveryAction {
	page := recoveryHelpPage{Title: "Repair Clawmeter configuration", Description: "Clawmeter could not load its configuration and will retry automatically. Run this command in a terminal to see the current error. Edit the configuration file to fix it, then leave the tray running.", Commands: []string{"clawmeter config show"}}
	if dir, err := os.UserConfigDir(); err == nil {
		page.Description += " Configuration file: " + filepath.Join(dir, "clawmeter", "config.yaml")
	}
	return recoveryAction{help: page}
}

func openProviderHeader(menu *providerMenuItems, open func(string), connect func(string)) {
	trayRenderMu.Lock()
	visible := menu.headerState.visible && menu.headerState.enabled && !menu.disabled
	action, revision := menu.headerRecovery, menu.sourceRevision
	connects := menu.headerConnects.Load()
	trayRenderMu.Unlock()
	if !visible || !sourceStillCurrent(menu, revision) {
		return
	}
	if connects {
		connect(provider.SourceKey(menu.provider))
		return
	}
	if action.label != "" {
		openRecovery(action, open)
		return
	}
	if menu.opensDashboard {
		open(menu.provider.DashboardURL())
	}
}

func sourceStillCurrent(menu *providerMenuItems, revision string) bool {
	return revision == "" || provider.SourceRevision(menu.provider) == revision
}

func openResetRow(menu *providerMenuItems, open func(string)) {
	trayRenderMu.Lock()
	target, revision := menu.resetURL, menu.sourceRevision
	visible := menu.resetState.visible && menu.resetState.enabled && !menu.disabled
	trayRenderMu.Unlock()
	if visible && target != "" && sourceStillCurrent(menu, revision) {
		open(target)
	}
}

func openStatusRow(menu *providerMenuItems, open func(string)) {
	trayRenderMu.Lock()
	target, refresh, revision := menu.statusURL, menu.refreshUsage, menu.sourceRevision
	visible := menu.statusState.visible && menu.statusState.enabled && !menu.disabled
	trayRenderMu.Unlock()
	if !visible || !sourceStillCurrent(menu, revision) {
		return
	}
	if target != "" {
		open(target)
	} else if refresh != nil {
		refresh()
	}
}

// A manual request waits for the active poll and coalesces further clicks.
// Automatic polls retain their existing non-queuing behavior.
type manualRefreshAction struct{ pending atomic.Bool }

func (a *manualRefreshAction) request(run func()) bool {
	if !a.pending.CompareAndSwap(false, true) {
		return false
	}
	go func() { defer a.pending.Store(false); run() }()
	return true
}

func updateRecoveryError(err error) recoveryAction {
	page := recoveryHelpPage{Title: "Update Clawmeter", Description: fmt.Sprint(err), Commands: []string{"clawmeter update"}}
	if errors.Is(err, update.ErrHomebrewManaged) || errors.Is(err, update.ErrHomebrewReceiptUnverified) {
		page.Description = "This installation is owned by Homebrew or is inside a Homebrew package directory whose ownership cannot safely be ruled out. Use Homebrew to upgrade it, then restart Clawmeter. If Homebrew cannot repair it, check the installation before replacing files."
		page.Commands = []string{"brew upgrade clawmeter"}
	}
	return recoveryAction{help: page}
}
