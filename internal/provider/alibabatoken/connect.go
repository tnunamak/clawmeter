package alibabatoken

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tnunamak/clawmeter/internal/config"
)

const (
	// OfficialCLIInstallCommand is intentionally an explicit, user-visible
	// install command. Clawmeter does not silently download executable code.
	OfficialCLIInstallCommand = "npm install --global bailian-cli"
	OfficialCLIURL            = "https://www.alibabacloud.com/help/zh/model-studio/use-model-studio-cli"
	TokenPlanConnectCommand   = "clawmeter providers connect token-plan"
)

type cliLookup func(string) (string, error)
type cliRunner func(context.Context, string, io.Writer, io.Writer) error

// Connect starts Alibaba's documented browser authorization flow when the
// local console session is missing. The API key is deliberately not used for
// this operation: it authorizes model requests, not account-level plan quota.
// The flow is explicit because it mutates the official CLI's local auth store.
func Connect(ctx context.Context, force bool, stdout, stderr io.Writer) error {
	return connect(ctx, force, stdout, stderr, exec.LookPath, runConsoleLogin)
}

func connect(ctx context.Context, force bool, stdout, stderr io.Writer, lookup cliLookup, run cliRunner) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	if !force && New(config.ProviderConfig{}).IsConfigured() {
		fmt.Fprintln(stdout, "Alibaba Token Plan quota access is already connected.")
		fmt.Fprintln(stdout, "Run `clawmeter token-plan` to read the current quota.")
		fmt.Fprintln(stdout, "If the quota reports an expired session, rerun with `--force`.")
		return nil
	}

	executable, err := findBailianCLI(lookup)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Opening Alibaba Model Studio authorization with %s...\n", filepath.Base(executable))
	if err := run(ctx, executable, stdout, stderr); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Alibaba Model Studio authorization did not complete: %w", ctx.Err())
		}
		return fmt.Errorf("Alibaba Model Studio authorization did not complete: %w", err)
	}

	if !New(config.ProviderConfig{}).IsConfigured() {
		return fmt.Errorf("Alibaba authorization finished, but no console session was found; rerun %s or check the official CLI setup", TokenPlanConnectCommand)
	}

	fmt.Fprintln(stdout, "Alibaba Token Plan quota access connected.")
	fmt.Fprintln(stdout, "Run `clawmeter token-plan` to read the current quota.")
	return nil
}

func findBailianCLI(lookup cliLookup) (string, error) {
	// `bl` is the command documented by Alibaba's official package.
	if path, err := lookup("bl"); err == nil && strings.TrimSpace(path) != "" {
		return path, nil
	}
	return "", fmt.Errorf("Alibaba's official Bailian CLI is not installed or is not on PATH\n\nInstall it once:\n  %s\nThen rerun:\n  %s\nDocs: %s\n\nBAILIAN_TOKEN_PLAN_API_KEY is for model requests; it cannot provide account-level Token Plan quota", OfficialCLIInstallCommand, TokenPlanConnectCommand, OfficialCLIURL)
}
