package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/autostart"
	"github.com/tnunamak/clawmeter/internal/cli"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	"github.com/tnunamak/clawmeter/internal/tray"
	"github.com/tnunamak/clawmeter/internal/update"
)

var Version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	if handled, code := update.HandleRestartHelper(os.Args[1:]); handled {
		return code
	}

	update.CleanupOld()

	if len(os.Args) < 2 {
		return cli.Status(false, false, false)
	}

	// Handle documented top-level status flags (for example:
	// clawmeter --json, clawmeter --plain, clawmeter --all).
	if isStatusShortcutFlag(os.Args[1]) {
		return statusCmd(os.Args[1:])
	}

	switch os.Args[1] {
	case "status":
		return statusCmd(os.Args[2:])
	case "statusline":
		return statuslineCmd(os.Args[2:])
	case "setup":
		return setupCmd(os.Args[2:])
	case "doctor":
		return doctorCmd(os.Args[2:])
	case "tray":
		return trayCmd(os.Args[2:])
	case "config":
		return configCmd(os.Args[2:])
	case "providers":
		return providersCmd(os.Args[2:])
	case "update":
		return updateCmd()
	case "version", "--version", "-v":
		fmt.Println("clawmeter " + Version)
		return 0
	case "help", "--help", "-h":
		printHelp(os.Stdout)
		return 0
	default:
		// Check if it's a provider name (e.g., "clawmeter claude --json")
		if providerName, ok := all.CanonicalName(os.Args[1]); ok && newRegistry().Has(providerName) {
			return providerCmd(providerName, os.Args[2:])
		}
		fmt.Fprintf(os.Stderr, "clawmeter: unknown command %q\n", os.Args[1])
		printHelp(os.Stderr)
		return 1
	}
}

func isStatusShortcutFlag(arg string) bool {
	switch arg {
	case "--json", "-json",
		"--plain", "-plain",
		"--agent", "-agent",
		"--check", "-check",
		"--all", "-all",
		"--provider", "-provider":
		return true
	default:
		return false
	}
}

// newRegistry creates a registry with all providers registered.
func newRegistry() *provider.Registry {
	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}
	registry := provider.NewRegistry()
	all.Register(registry, cfg)
	return registry
}

func statusCmd(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonMode := fs.Bool("json", false, "output JSON")
	plainMode := fs.Bool("plain", false, "plain text (no color)")
	agentMode := fs.Bool("agent", false, "token-efficient all-quota summary for AI agents")
	checkMode := fs.Bool("check", false, "exit 0=healthy, 1=warning, 2=critical/expired/error")
	providerFlag := fs.String("provider", "", "show only specific provider")
	showAll := fs.Bool("all", false, "show all providers including unavailable ones")
	fs.Parse(args)

	if *checkMode {
		return cli.Check()
	}
	if *agentMode {
		return cli.StatusAgent(*showAll)
	}
	if *providerFlag != "" {
		return cli.SingleProviderStatus(*providerFlag, *jsonMode, *plainMode)
	}
	return cli.Status(*jsonMode, *plainMode, *showAll)
}

func statuslineCmd(args []string) int {
	fs := flag.NewFlagSet("statusline", flag.ExitOnError)
	showAll := fs.Bool("all", false, "include unavailable providers")
	fs.Parse(args)
	return cli.StatusLine(*showAll)
}

func providerCmd(providerName string, args []string) int {
	fs := flag.NewFlagSet(providerName, flag.ExitOnError)
	jsonMode := fs.Bool("json", false, "output JSON")
	plainMode := fs.Bool("plain", false, "plain text (no color)")
	fs.Parse(args)

	return cli.SingleProviderStatus(providerName, *jsonMode, *plainMode)
}

func trayCmd(args []string) int {
	fs := flag.NewFlagSet("tray", flag.ExitOnError)
	install := fs.Bool("install", false, "enable launch at login")
	uninstall := fs.Bool("uninstall", false, "disable launch at login")
	fs.Parse(args)

	if *install {
		if err := autostart.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
			return 1
		}
		fmt.Println("clawmeter will start at login")
		return 0
	}
	if *uninstall {
		if err := autostart.Uninstall(); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
			return 1
		}
		fmt.Println("clawmeter autostart removed")
		return 0
	}

	prepareTrayConsole()
	return tray.Run(Version)
}

func setupCmd(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	allFlag := fs.Bool("all", false, "install supported local integrations")
	tmuxFlag := fs.Bool("tmux", false, "install tmux status-right integration")
	claudeFlag := fs.Bool("claude-statusline", false, "install Claude Code statusline integration")
	dryRun := fs.Bool("dry-run", false, "show changes without writing files or tmux settings")
	fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: setup does not take positional arguments\n")
		return 1
	}

	if *allFlag {
		*claudeFlag = true
	}
	if *tmuxFlag || *claudeFlag {
		fmt.Println("Clawmeter setup")
		fmt.Println()
		if *tmuxFlag {
			printIntegrationResult(setupTmuxIntegration(*dryRun))
		}
		if *claudeFlag {
			printIntegrationResult(setupClaudeStatuslineIntegration(*dryRun))
		}
		fmt.Println()
		fmt.Println("Agent pull command: clawmeter status --agent")
		fmt.Println("Run `clawmeter doctor` to verify provider auth and integrations.")
		return 0
	}

	fmt.Println("Clawmeter setup")
	fmt.Println()
	fmt.Println("Install supported local integrations:")
	fmt.Println("  clawmeter setup --all")
	fmt.Println()
	fmt.Println("Install individual or advanced integrations:")
	fmt.Println("  clawmeter setup --claude-statusline")
	fmt.Println("  clawmeter setup --tmux")
	fmt.Println()
	fmt.Println("Start surfaces:")
	fmt.Println("  clawmeter tray --install")
	fmt.Println("  clawmeter statusline")
	fmt.Println("  clawmeter status --agent")
	fmt.Println()
	fmt.Println("Use `--dry-run` to preview setup writes.")
	fmt.Println("Run `clawmeter doctor` to verify provider auth and integrations.")
	return 0
}

func doctorCmd(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "clawmeter: doctor does not take positional arguments\n")
		return 1
	}

	fmt.Println("Clawmeter doctor")
	fmt.Println()
	providersCmd(nil)
	fmt.Println("Integrations:")
	printIntegrationResult(tmuxIntegrationStatus())
	printIntegrationResult(claudeStatuslineStatus())
	fmt.Println("  statusline command:      clawmeter statusline")
	fmt.Println("  agent pull command:      clawmeter status --agent")
	return 0
}

func configCmd(args []string) int {
	if len(args) < 1 {
		printConfigHelp(os.Stderr)
		return 1
	}

	switch args[0] {
	case "help", "--help", "-h":
		printConfigHelp(os.Stdout)
		return 0
	case "show":
		return configShowCmd(args[1:])
	case "set":
		return configSetCmd(args[1:])
	case "enable":
		return configEnableCmd(args[1:], true)
	case "disable":
		return configEnableCmd(args[1:], false)
	default:
		printConfigHelp(os.Stderr)
		return 1
	}
}

func configShowCmd(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	fmt.Println("Providers (config entries):")
	if len(cfg.Providers) == 0 {
		fmt.Println("  (none — detected providers run by default)")
	}
	for name, pc := range cfg.Providers {
		state := "disabled"
		if pc.Enabled {
			state = "enabled"
		}
		marker := ""
		if !all.IsCanonicalName(name) {
			marker = "  (unknown provider name — ignored)"
		}
		fmt.Printf("  %s: %s%s\n", name, state, marker)
		if pc.APIKey != "" {
			show := pc.APIKey
			if len(show) > 4 {
				show = show[:4] + "****"
			}
			fmt.Printf("    API key: %s\n", show)
		}
		if pc.OAuthToken != "" {
			show := pc.OAuthToken
			if len(show) > 4 {
				show = show[:4] + "****"
			}
			fmt.Printf("    OAuth token: %s\n", show)
		}
	}

	fmt.Printf("\nSettings:\n")
	fmt.Printf("  Poll interval: %d seconds\n", cfg.Settings.PollInterval)
	fmt.Printf("  Check for updates: %t\n", cfg.ShouldCheckForUpdates())
	fmt.Printf("  Warning threshold: %.0f%%\n", cfg.Settings.NotificationThresholds.Warning)
	fmt.Printf("  Critical threshold: %.0f%%\n", cfg.Settings.NotificationThresholds.Critical)

	return 0
}

func configSetCmd(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: clawmeter config set <key> <value>")
		fmt.Fprintln(os.Stderr, "  poll_interval <seconds>")
		fmt.Fprintln(os.Stderr, "  warning_threshold <percent>")
		fmt.Fprintln(os.Stderr, "  critical_threshold <percent>")
		fmt.Fprintln(os.Stderr, "  check_for_updates <true|false>")
		return 1
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	key, value := args[0], args[1]
	switch key {
	case "poll_interval":
		var seconds int
		if _, err := fmt.Sscanf(value, "%d", &seconds); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: invalid value %q\n", value)
			return 1
		}
		if seconds < 60 {
			fmt.Fprintf(os.Stderr, "clawmeter: poll_interval must be >= 60 seconds\n")
			return 1
		}
		cfg.Settings.PollInterval = seconds
	case "warning_threshold":
		var pct float64
		if _, err := fmt.Sscanf(value, "%f", &pct); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: invalid value %q\n", value)
			return 1
		}
		if pct < 0 || pct > 100 {
			fmt.Fprintf(os.Stderr, "clawmeter: warning_threshold must be 0-100\n")
			return 1
		}
		cfg.Settings.NotificationThresholds.Warning = pct
	case "critical_threshold":
		var pct float64
		if _, err := fmt.Sscanf(value, "%f", &pct); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: invalid value %q\n", value)
			return 1
		}
		if pct < 0 || pct > 100 {
			fmt.Fprintf(os.Stderr, "clawmeter: critical_threshold must be 0-100\n")
			return 1
		}
		if pct <= cfg.Settings.NotificationThresholds.Warning {
			fmt.Fprintf(os.Stderr, "clawmeter: critical_threshold must be greater than warning_threshold (%.0f)\n", cfg.Settings.NotificationThresholds.Warning)
			return 1
		}
		cfg.Settings.NotificationThresholds.Critical = pct
	case "check_for_updates":
		var enabled bool
		if _, err := fmt.Sscanf(value, "%t", &enabled); err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: invalid value %q\n", value)
			return 1
		}
		cfg.Settings.CheckForUpdates = &enabled
	default:
		fmt.Fprintf(os.Stderr, "clawmeter: unknown config key %q\n", key)
		return 1
	}

	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return 0
}

func configEnableCmd(args []string, enable bool) int {
	action := "enable"
	if !enable {
		action = "disable"
	}
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: clawmeter config %s <provider>\n", action)
		fmt.Fprintf(os.Stderr, "Known providers: %s\n", strings.Join(all.Names(), ", "))
		return 1
	}

	providerName, ok := all.CanonicalName(args[0])

	if !ok {
		fmt.Fprintf(os.Stderr, "clawmeter: unknown provider %q\n", args[0])
		if suggestion := all.Suggest(args[0]); suggestion != "" {
			fmt.Fprintf(os.Stderr, "  did you mean %q?\n", suggestion)
		}
		fmt.Fprintf(os.Stderr, "  known providers: %s\n", strings.Join(all.Names(), ", "))
		return 1
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	cfg.EnsureProvider(providerName, enable)

	if err := cfg.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	verb := "Enabled"
	if !enable {
		verb = "Disabled"
	}
	fmt.Printf("%s provider: %s\n", verb, providerName)

	if enable {
		if p, ok := newRegistry().Get(providerName); ok {
			st := provider.GetSetupStatus(p)
			if !st.IsReady() {
				if st.Detail != "" {
					fmt.Printf("! %s: %s\n", providerName, st.Detail)
				} else {
					fmt.Printf("! %s: setup needed\n", providerName)
				}
				if url := p.DashboardURL(); url != "" {
					fmt.Printf("  dashboard: %s\n", url)
				}
			}
		}
	}
	return 0
}

func providersCmd(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "enable":
			return configEnableCmd(args[1:], true)
		case "disable":
			return configEnableCmd(args[1:], false)
		case "help", "--help", "-h":
			fmt.Println("Usage: clawmeter providers [enable|disable <provider>]")
			fmt.Println()
			fmt.Println("Without arguments, lists provider auth status.")
			fmt.Println("Detected providers run automatically. Enable is for opt-in providers or manual overrides.")
			fmt.Println("Examples:")
			fmt.Println("  clawmeter providers")
			fmt.Println("  clawmeter grok")
			fmt.Println("  clawmeter providers enable openrouter")
			fmt.Println("  clawmeter providers disable codex")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "clawmeter: unknown providers command %q\n", args[0])
			fmt.Fprintln(os.Stderr, "Usage: clawmeter providers [enable|disable <provider>]")
			return 1
		}
	}

	registry := newRegistry()

	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}

	fmt.Println("Available providers:")
	fmt.Println()

	for _, p := range registry.GetAll() {
		st := describeProviderState(p, cfg)

		indicator := "○"
		if st == "enabled" || st == "detected" {
			indicator = "●"
		}

		maturity := provider.GetMaturity(p.Name())
		fmt.Printf("  %s %s (%s, %s)\n", indicator, p.DisplayName(), st, maturity.Level)
		fmt.Printf("      %s\n", p.Description())
		fmt.Printf("      learn more: %s\n", maturity.LearnMore)
		fmt.Println()
	}

	fmt.Println("Legend:")
	fmt.Println("  detected      credentials found, will be polled")
	fmt.Println("  enabled       explicitly enabled in config, will be polled")
	fmt.Println("  available     credentials found; enable to poll")
	fmt.Println("  setup needed  installed or enabled, but missing usable auth")
	fmt.Println("  disabled      explicitly disabled in config, will NOT be polled")
	fmt.Println("  no credentials  no credentials detected; nothing to poll")
	fmt.Println()
	fmt.Println("Detected/enabled providers are polled automatically.")
	fmt.Println("Use 'clawmeter providers enable <provider>' to opt available providers in,")
	fmt.Println("or 'clawmeter providers disable <provider>' to opt out.")
	return 0
}

// describeProviderState returns the user-facing summary of how a provider will
// be treated by default polling and explicit config.
func describeProviderState(p provider.Provider, cfg *config.Config) string {
	pc, hasEntry := cfg.Providers[p.Name()]
	setup := provider.GetSetupStatus(p)
	autoPoll := provider.AutoPollByDefault(p)
	switch {
	case hasEntry && !pc.Enabled:
		return "disabled"
	case hasEntry && pc.Enabled && setup.IsReady():
		return "enabled"
	case hasEntry && pc.Enabled:
		if setup.Detail != "" {
			return "enabled, setup needed: " + setup.Detail
		}
		return "enabled, setup needed"
	case setup.IsReady() && autoPoll:
		return "detected"
	case setup.IsReady():
		return "available, enable to poll"
	case setup.State == provider.SetupNeedsAuth:
		if setup.Detail != "" {
			return "setup needed: " + setup.Detail
		}
		return "setup needed"
	default:
		return "no credentials"
	}
}

func updateCmd() int {
	if Version == "dev" {
		fmt.Fprintln(os.Stderr, "clawmeter: self-update is not available for dev builds")
		return 1
	}

	fmt.Printf("Current version: %s\n", Version)
	fmt.Print("Checking for updates... ")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := update.Check(ctx, Version)
	if err != nil {
		fmt.Println()
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}
	if rel == nil {
		fmt.Println("already up to date.")
		return 0
	}

	fmt.Printf("found %s\n", rel.Version)
	fmt.Printf("Downloading and installing %s... ", rel.Version)

	if err := update.Apply(ctx, rel.URL); err != nil {
		fmt.Println()
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	fmt.Println("done.")
	fmt.Printf("Updated to %s. Restart any running tray instances.\n", rel.Version)
	return 0
}

func printHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage: clawmeter [command] [flags]

Commands:
  status                    Show usage for all configured providers (default)
  statusline                Print a compact statusline segment
  <provider>                Show usage for a specific provider
  providers                 List available providers
  setup                     Install or show local integrations
  doctor                    Check provider and integration readiness
  tray                      Run as system tray icon
  config                    Manage configuration
  update                    Self-update to the latest release
  version                   Show version
  help                      Show this help

Status flags:
  --json                    Output as JSON
  --plain                   Plain text, no color codes
  --agent                   Token-efficient all-quota summary for AI agents
  --check                   Exit 0=healthy, 1=warning, 2=critical/error
  --provider <name>         Show only specific provider
  --all                     Include unavailable providers

Config commands:
  config show               Show current configuration
  config set <key> <value>  Set a configuration value
  config enable <provider>  Enable a provider
  config disable <provider> Disable a provider
  providers enable <provider>
                            Enable a provider alias from provider listing

Tray flags:
  --install                 Enable launch at login
  --uninstall               Disable launch at login

Examples:
  clawmeter                          # Show all providers
  clawmeter statusline               # Compact shell/tmux/statusline segment
  clawmeter status --agent           # Token-efficient all-quota summary
  clawmeter claude --json            # Show Claude usage as JSON
  clawmeter --check                  # Exit code for monitoring
  clawmeter setup --all              # Install mainstream local integrations
  clawmeter codex                    # Show Codex quota
  clawmeter grok                     # Show Grok quota after grok login
  clawmeter providers                # List available providers`)
}

func printConfigHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage: clawmeter config <command>

Commands:
  show                      Show current configuration
  set <key> <value>         Set a configuration value
  enable <provider>         Enable a provider
  disable <provider>        Disable a provider

Settable keys:
  poll_interval <seconds>   Tray polling interval (default: 300)
  warning_threshold <%>     Notification warning threshold (default: 80)
  critical_threshold <%>    Notification critical threshold (default: 95)
  check_for_updates <bool>  Automatic GitHub release checks (default: true)

Examples:
  clawmeter config show
  clawmeter config set poll_interval 600
  clawmeter config set check_for_updates false
  clawmeter config enable openrouter
  clawmeter providers enable openrouter
  clawmeter config disable claude`)
}
