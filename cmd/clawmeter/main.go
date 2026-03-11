package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tnunamak/clawmeter/internal/autostart"
	"github.com/tnunamak/clawmeter/internal/cli"
	"github.com/tnunamak/clawmeter/internal/config"
	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/provider/all"
	"github.com/tnunamak/clawmeter/internal/tray"
)

var Version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		return cli.Status(false, false, false)
	}

	// Handle top-level flags (clawmeter --json, clawmeter --plain, clawmeter --check)
	if os.Args[1] == "--json" || os.Args[1] == "--plain" || os.Args[1] == "--check" {
		return statusCmd(os.Args[1:])
	}

	switch os.Args[1] {
	case "status":
		return statusCmd(os.Args[2:])
	case "tray":
		return trayCmd(os.Args[2:])
	case "config":
		return configCmd(os.Args[2:])
	case "providers":
		return providersCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("clawmeter " + Version)
		return 0
	case "help", "--help", "-h":
		printHelp()
		return 0
	default:
		// Check if it's a provider name (e.g., "clawmeter claude --json")
		if newRegistry().Has(os.Args[1]) {
			return providerCmd(os.Args[1], os.Args[2:])
		}
		fmt.Fprintf(os.Stderr, "clawmeter: unknown command %q\n", os.Args[1])
		printHelp()
		return 1
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
	checkMode := fs.Bool("check", false, "exit 0=healthy, 1=warning, 2=critical/expired/error")
	providerFlag := fs.String("provider", "", "show only specific provider")
	showAll := fs.Bool("all", false, "show all providers including unavailable ones")
	fs.Parse(args)

	if *checkMode {
		return cli.Check()
	}
	if *providerFlag != "" {
		return cli.SingleProviderStatus(*providerFlag, *jsonMode, *plainMode)
	}
	return cli.Status(*jsonMode, *plainMode, *showAll)
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

	return tray.Run(Version)
}

func configCmd(args []string) int {
	if len(args) < 1 {
		printConfigHelp()
		return 1
	}

	switch args[0] {
	case "show":
		return configShowCmd(args[1:])
	case "set":
		return configSetCmd(args[1:])
	case "enable":
		return configEnableCmd(args[1:], true)
	case "disable":
		return configEnableCmd(args[1:], false)
	default:
		printConfigHelp()
		return 1
	}
}

func configShowCmd(args []string) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: %v\n", err)
		return 1
	}

	fmt.Println("Providers:")
	for name, pc := range cfg.Providers {
		status := "disabled"
		if pc.Enabled {
			status = "enabled"
		}
		fmt.Printf("  %s: %s\n", name, status)
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
	if len(args) < 1 {
		action := "enable"
		if !enable {
			action = "disable"
		}
		fmt.Fprintf(os.Stderr, "Usage: clawmeter config %s <provider>\n", action)
		return 1
	}

	providerName := args[0]

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

	action := "Enabled"
	if !enable {
		action = "Disabled"
	}
	fmt.Printf("%s provider: %s\n", action, providerName)
	return 0
}

func providersCmd(args []string) int {
	registry := newRegistry()

	cfg, err := config.Load()
	if err != nil {
		cfg = config.DefaultConfig()
	}

	fmt.Println("Available providers:")
	fmt.Println()

	for _, p := range registry.GetAll() {
		st := "not configured"
		if pc, ok := cfg.Providers[p.Name()]; ok {
			if pc.Enabled {
				st = "enabled"
			} else {
				st = "disabled"
			}
		} else if p.IsConfigured() {
			st = "detected"
		}

		indicator := "○"
		if st == "enabled" || st == "detected" {
			indicator = "●"
		}

		fmt.Printf("  %s %s (%s)\n", indicator, p.DisplayName(), st)
		fmt.Printf("      %s\n", p.Description())
		fmt.Println()
	}

	fmt.Println("Use 'clawmeter config enable <provider>' to enable a provider.")
	fmt.Println("Detected providers will be auto-enabled when you run 'clawmeter'.")
	return 0
}

func printHelp() {
	fmt.Fprintln(os.Stderr, `Usage: clawmeter [command] [flags]

Commands:
  status                    Show usage for all configured providers (default)
  <provider>                Show usage for a specific provider
  providers                 List available providers
  tray                      Run as system tray icon
  config                    Manage configuration
  version                   Show version
  help                      Show this help

Status flags:
  --json                    Output as JSON
  --plain                   Plain text, no color codes
  --check                   Exit 0=healthy, 1=warning, 2=critical/error
  --provider <name>         Show only specific provider

Config commands:
  config show               Show current configuration
  config set <key> <value>  Set a configuration value
  config enable <provider>  Enable a provider
  config disable <provider> Disable a provider

Tray flags:
  --install                 Enable launch at login
  --uninstall               Disable launch at login

Examples:
  clawmeter                          # Show all providers
  clawmeter claude --json            # Show Claude usage as JSON
  clawmeter --check                  # Exit code for monitoring
  clawmeter config enable openai     # Enable OpenAI provider
  clawmeter providers                # List available providers`)
}

func printConfigHelp() {
	fmt.Fprintln(os.Stderr, `Usage: clawmeter config <command>

Commands:
  show                      Show current configuration
  set <key> <value>         Set a configuration value
  enable <provider>         Enable a provider
  disable <provider>        Disable a provider

Settable keys:
  poll_interval <seconds>   Tray polling interval (default: 300)
  warning_threshold <%>     Notification warning threshold (default: 80)
  critical_threshold <%>    Notification critical threshold (default: 95)

Examples:
  clawmeter config show
  clawmeter config set poll_interval 600
  clawmeter config enable openai
  clawmeter config disable claude`)
}
