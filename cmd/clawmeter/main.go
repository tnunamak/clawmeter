package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tnunamak/clawmeter/internal/cli"
	"github.com/tnunamak/clawmeter/internal/tray"
)

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
		return trayCmd()
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

func trayCmd() int {
	return tray.Run()
}

func printHelp() {
	fmt.Fprintln(os.Stderr, `Usage: clawmeter [command] [flags]

Commands:
  status    Show current usage (default)
  tray      Run as system tray icon
  help      Show this help

Status flags:
  --json    Output as JSON
  --plain   Plain text, no color codes`)
}
