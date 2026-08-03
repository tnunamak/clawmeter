//go:build windows

package alibabatoken

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runConsoleLogin(ctx context.Context, executable string, stdout, stderr io.Writer) error {
	args := []string{"auth", "login", "--console"}
	if ext := strings.ToLower(filepath.Ext(executable)); ext == ".cmd" || ext == ".bat" {
		// npm exposes global commands as .cmd shims on Windows. Invoke the shim
		// through cmd.exe so the same Clawmeter command works from PowerShell,
		// Command Prompt, and the Windows terminal.
		command := "call " + quoteWindowsArg(executable)
		for _, arg := range args {
			command += " " + quoteWindowsArg(arg)
		}
		cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", command)
		cmd.Stdin = os.Stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		return cmd.Run()
	}

	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func quoteWindowsArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
