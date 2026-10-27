package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const cliUsageTimeout = 25 * time.Second

// fetchCLIUsage reads the CLI's structured built-in usage command. It does not
// ask the model to produce a report. The CLI owns its normal login and settings.
func (p *Provider) fetchCLIUsage(ctx context.Context) (*provider.UsageData, error) {
	path, err := p.executablePath("agy")
	if err != nil {
		return nil, fmt.Errorf("Antigravity CLI unavailable")
	}

	runCtx, cancel := context.WithTimeout(ctx, cliUsageTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, path, "--print", "/usage", "--output-format", "json", "--print-timeout", "20s")
	configureQuotaCommand(cmd)
	cmd.WaitDelay = time.Second
	hideSubprocessWindow(cmd)
	tmp, err := os.MkdirTemp("", "clawmeter-antigravity-usage-")
	if err != nil {
		return nil, fmt.Errorf("cannot create private Antigravity CLI workspace")
	}
	defer os.RemoveAll(tmp)
	cmd.Dir = tmp
	if err := installBrowserBlockers(tmp); err != nil {
		return nil, fmt.Errorf("cannot isolate Antigravity CLI browser launchers")
	}
	cmd.Env = append(os.Environ(), "AGY_CLI_DISABLE_AUTO_UPDATE=true", "BROWSER=/bin/false", "PATH="+tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdout = &limitedBuffer{limit: maxResponseBytes}
	// Never expose CLI stderr: it can contain account and local path details.
	cmd.Stderr = io.Discard
	cleanup, err := startQuotaCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("Antigravity CLI usage command could not start")
	}
	defer cleanup()
	if err := cmd.Wait(); err != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("Antigravity CLI usage command timed out or was cancelled")
		}
		return nil, fmt.Errorf("Antigravity CLI usage command failed")
	}
	out := cmd.Stdout.(*limitedBuffer)
	if out.overflow {
		return nil, fmt.Errorf("Antigravity CLI usage output exceeded limit")
	}
	var report struct {
		Status   string          `json:"status"`
		NumTurns *int            `json:"num_turns"`
		Usage    map[string]*int `json:"usage"`
		Command  struct {
			Name string `json:"name"`
			Data struct {
				Groups []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Buckets     []struct {
						ID          string   `json:"id"`
						Name        string   `json:"name"`
						Description string   `json:"description"`
						Window      string   `json:"window"`
						Remaining   *float64 `json:"remaining_fraction"`
						Reset       string   `json:"reset_time"`
						Disabled    bool     `json:"disabled"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"data"`
		} `json:"command"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		return nil, fmt.Errorf("invalid Antigravity CLI usage report")
	}
	zero := func(n *int) bool { return n != nil && *n == 0 }
	accountingValid := zero(report.NumTurns) && zero(report.Usage["input_tokens"]) && zero(report.Usage["output_tokens"]) && zero(report.Usage["total_tokens"])
	for _, count := range report.Usage {
		accountingValid = accountingValid && zero(count)
	}
	if !strings.EqualFold(report.Status, "SUCCESS") || !accountingValid || !strings.EqualFold(report.Command.Name, "usage") || report.Command.Data.Groups == nil {
		return nil, fmt.Errorf("Antigravity CLI did not return a successful usage report")
	}
	summary := quotaSummary{}
	for _, group := range report.Command.Data.Groups {
		g := quotaGroup{DisplayName: group.Name, Description: group.Description}
		for _, bucket := range group.Buckets {
			g.Buckets = append(g.Buckets, quotaBucket{BucketID: bucket.ID, DisplayName: bucket.Name, Description: bucket.Description, RemainingFraction: bucket.Remaining, ResetTime: bucket.Reset, Window: bucket.Window, Disabled: bucket.Disabled})
		}
		summary.Groups = append(summary.Groups, g)
	}
	body, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("cannot normalize Antigravity CLI usage report")
	}
	windows, err := parseQuotaSummary(body, p.now())
	if err != nil {
		return nil, err
	}
	return &provider.UsageData{Provider: p.Name(), FetchedAt: p.now(), Windows: windows}, nil
}

func installBrowserBlockers(dir string) error {
	for _, name := range []string{"xdg-open", "open"} {
		path := dir + string(os.PathSeparator) + name
		body := "#!/bin/sh\nexit 1\n"
		mode := os.FileMode(0700)
		if runtime.GOOS == "windows" {
			path += ".cmd"
			body = "@exit /b 1\r\n"
			mode = 0600
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			return err
		}
	}
	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.overflow = true
		_, _ = b.Buffer.Write(p[:remaining])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}
