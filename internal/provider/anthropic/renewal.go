package anthropic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	renewalTimeout    = 30 * time.Second
	renewalCooldown   = 5 * time.Minute
	expiredLoginError = "login expired · run `claude` to renew it"
)

var (
	doctorRunner = runClaudeDoctor
	renewalNow   = time.Now
)

type renewalAttempt struct {
	AttemptedAt time.Time `json:"attempted_at"`
	Fingerprint string    `json:"fingerprint"`
}

func (p *Provider) expiredUsage() *provider.UsageData {
	return &provider.UsageData{
		Provider: p.Name(), SourceID: p.SourceID(), SourceLabel: p.SourceLabel(),
		FetchedAt: time.Now(), IsExpired: true, Error: expiredLoginError,
	}
}

func (p *Provider) renewLogin(ctx context.Context, old *Credentials) *Credentials {
	if old.tokenOnly != "" || old.ClaudeAiOauth.RefreshToken == "" || ctx.Err() != nil {
		return nil
	}
	path, err := renewalStatePath(old)
	if err != nil {
		return nil
	}
	lock, err := lockRenewalState(path)
	if err != nil {
		return nil
	}
	defer lock.Close()
	fingerprint := loginFingerprint(old)
	var attempt renewalAttempt
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &attempt) != nil {
			return nil
		}
		if attempt.Fingerprint == fingerprint && renewalNow().Sub(attempt.AttemptedAt) < renewalCooldown {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return nil
	}

	// Another fetch may have renewed this exact source while we waited.
	if fresh, err := rereadLogin(old); err == nil && freshLogin(old, fresh) {
		return fresh
	}
	workDir, err := os.MkdirTemp("", "clawmeter-claude-doctor-*")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(workDir)

	// Reserve the attempt before launching, so other invocations see it too.
	attempt = renewalAttempt{AttemptedAt: renewalNow(), Fingerprint: fingerprint}
	if writeRenewalAttempt(path, attempt) != nil {
		return nil
	}
	doctorCtx, cancel := context.WithTimeout(ctx, renewalTimeout)
	defer cancel()
	_ = doctorRunner(doctorCtx, workDir, p.doctorEnvironment(old))
	fresh, err := rereadLogin(old)
	if err == nil && freshLogin(old, fresh) {
		_ = os.Remove(path)
		return fresh
	}
	if err == nil {
		attempt.Fingerprint = loginFingerprint(fresh)
		_ = writeRenewalAttempt(path, attempt)
	}
	return nil
}

func freshLogin(old, fresh *Credentials) bool {
	return fresh.AccessToken() != "" && !fresh.IsExpired() &&
		(fresh.AccessToken() != old.AccessToken() || fresh.ClaudeAiOauth.ExpiresAt != old.ClaudeAiOauth.ExpiresAt)
}

func rereadLogin(old *Credentials) (*Credentials, error) {
	if old.fromKeychain {
		return (&Provider{}).readKeychain()
	}
	return readCredentialsPath(old.readPath, old.readConfigDir)
}

func loginFingerprint(creds *Credentials) string {
	data, _ := json.Marshal([]any{creds.AccessToken(), creds.ClaudeAiOauth.RefreshToken, creds.ClaudeAiOauth.ExpiresAt})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func renewalStatePath(creds *Credentials) (string, error) {
	route := "file\x00" + provider.CanonicalPath(creds.readPath)
	if creds.fromKeychain {
		route = "keychain\x00Claude Code-credentials"
	} else if creds.readPath == "" {
		return "", fmt.Errorf("login source unavailable")
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "clawmeter", fmt.Sprintf("claude-renewal-%x.json", sha256.Sum256([]byte(route)))), nil
}

func lockRenewalState(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	// Keep the lock inode: unlinking it would let another process bypass it.
	if err := tryRenewalLock(file); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func writeRenewalAttempt(path string, attempt renewalAttempt) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".claude-renewal-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (p *Provider) doctorEnvironment(creds *Credentials) []string {
	env := os.Environ()
	if !p.explicitSource && !p.pinnedNative {
		return env
	}
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "CLAUDE_CONFIG_DIR") && !strings.EqualFold(name, "CLAUDE_CODE_OAUTH_TOKEN") {
			filtered = append(filtered, entry)
		}
	}
	if p.explicitSource {
		filtered = append(filtered, "CLAUDE_CONFIG_DIR="+creds.readConfigDir)
	}
	return filtered
}

func runClaudeDoctor(ctx context.Context, dir string, env []string) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return err
	}
	return doctorCommand(ctx, path, dir, env).Run()
}

func doctorCommand(ctx context.Context, path, dir string, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, "doctor")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	return cmd
}
