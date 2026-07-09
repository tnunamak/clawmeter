package openai

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tnunamak/clawmeter/internal/provider"
	"github.com/tnunamak/clawmeter/internal/shellpath"
)

var initShellPath = shellpath.Init

// authFile mirrors the schema codex CLI writes to $CODEX_HOME/auth.json
// (or ~/.codex/auth.json by default). The CLI accepts either a top-level
// OPENAI_API_KEY or a tokens object with access_token / refresh_token.
type authFile struct {
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       *struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

func (p *Provider) IsConfigured() bool {
	return p.SetupStatus().IsReady()
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	if _, err := codexExecutablePath(); err != nil {
		return provider.SetupStatus{
			State:  provider.SetupUnavailable,
			Detail: "codex CLI not installed — see https://github.com/openai/codex",
		}
	}

	auth, err := readAuthFile(codexHome())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "run `codex login` to sign in",
		}
	case err != nil:
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "codex auth file unreadable — run `codex login`",
		}
	}

	if strings.TrimSpace(auth.OpenAIAPIKey) != "" {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "Codex auth API key"}
	}
	if auth.Tokens != nil && strings.TrimSpace(auth.Tokens.AccessToken) != "" {
		return provider.SetupStatus{State: provider.SetupReady, Detail: "ChatGPT account"}
	}
	return provider.SetupStatus{
		State:  provider.SetupNeedsAuth,
		Detail: "codex auth file has no credentials — run `codex login`",
	}
}

func codexExecutablePath() (string, error) {
	path, err := exec.LookPath("codex")
	if err == nil {
		return path, nil
	}
	initShellPath()
	return exec.LookPath("codex")
}

func codexHome() string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if runtime.GOOS == "windows" {
			home = os.Getenv("USERPROFILE")
		}
	}
	return filepath.Join(home, ".codex")
}

func readAuthFile(dir string) (*authFile, error) {
	path := filepath.Join(dir, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var auth authFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, err
	}
	return &auth, nil
}
