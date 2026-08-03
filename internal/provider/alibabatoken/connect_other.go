//go:build !windows

package alibabatoken

import (
	"context"
	"io"
	"os"
	"os/exec"
)

func runConsoleLogin(ctx context.Context, executable string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, executable, "auth", "login", "--console")
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
