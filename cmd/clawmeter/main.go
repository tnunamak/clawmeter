package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tnunamak/clawmeter/internal/autostart"
	"github.com/tnunamak/clawmeter/internal/cli"
	"github.com/tnunamak/clawmeter/internal/tray"
)

var Version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		return cli.Status(false, false)
	}

	switch os.Args[1] {
	case "status":
		return statusCmd(os.Args[2:])
	case "tray":
		return trayCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("clawmeter " + Version)
		return 0
	case "help", "--help", "-h":
		printHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "clawmeter: unknown command %q\n", os.Args[1])
		printHelp()
		return 1
	}
}

func statusCmd(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonMode := fs.Bool("json", false, "output JSON")
	plainMode := fs.Bool("plain", false, "plain text (no color)")
	fs.Parse(args)
	return cli.Status(*jsonMode, *plainMode)
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

func printHelp() {
	fmt.Fprintln(os.Stderr, `Usage: clawmeter [command] [flags]

Commands:
  status    Show current usage (default)
  tray      Run as system tray icon
  version   Show version
  help      Show this help

Status flags:
  --json    Output as JSON
  --plain   Plain text, no color codes

Tray flags:
  --install    Enable launch at login
  --uninstall  Disable launch at login`)
}
