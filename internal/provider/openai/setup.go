package openai

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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

func (p *Provider) authDirectory() string {
	if p.explicitSource {
		return p.codexHome
	}
	return codexHome()
}

func (p *Provider) IsConfigured() bool {
	return p.SetupStatus().IsReady()
}

func (p *Provider) SetupStatus() provider.SetupStatus {
	auth, err := readAuthFile(p.authDirectory())
	switch {
	case errors.Is(err, os.ErrNotExist):
		if _, cliErr := codexExecutablePath(); cliErr != nil {
			return codexCLIUnavailableStatus()
		}
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "run `codex login` to sign in",
		}
	case err != nil:
		if _, cliErr := codexExecutablePath(); cliErr != nil {
			return codexCLIUnavailableStatus()
		}
		return provider.SetupStatus{
			State:  provider.SetupNeedsAuth,
			Detail: "codex auth file unreadable — run `codex login`",
		}
	}

	if strings.TrimSpace(auth.OpenAIAPIKey) != "" {
		if _, err := codexExecutablePath(); err != nil {
			return codexCLIUnavailableStatus()
		}
		return provider.SetupStatus{State: provider.SetupReady, Detail: "Codex auth API key"}
	}
	if auth.Tokens != nil && strings.TrimSpace(auth.Tokens.AccessToken) != "" {
		if _, err := codexExecutablePath(); err == nil {
			return provider.SetupStatus{State: provider.SetupReady, Detail: "ChatGPT account"}
		}
		return provider.SetupStatus{State: provider.SetupReady, Detail: "ChatGPT account (direct quota read)"}
	}
	return provider.SetupStatus{
		State:  provider.SetupNeedsAuth,
		Detail: "codex auth file has no credentials — run `codex login`",
	}
}

func codexCLIUnavailableStatus() provider.SetupStatus {
	return provider.SetupStatus{
		State:  provider.SetupUnavailable,
		Detail: "codex CLI not installed — required when ChatGPT auth is unavailable",
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

// replaceEnv sets one environment variable without leaving an ambient value
// behind. Windows environment names are case-insensitive; Unix names are not.
func replaceEnv(env []string, key, value string, windows bool) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && ((windows && strings.EqualFold(name, key)) || (!windows && name == key)) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, key+"="+value)
}

func (p *Provider) sourceRevision() string {
	if !p.explicitSource {
		return ""
	}
	path := filepath.Join(p.codexHome, "auth.json")
	canonical, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	info, err := os.Stat(path)
	metadata := "unavailable"
	if err == nil {
		metadata = fmt.Sprintf("%d\x00%d\x00%o", info.Size(), info.ModTime().UnixNano(), info.Mode().Perm())
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(canonical+"\x00"+metadata)))
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
