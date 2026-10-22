package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tnunamak/clawmeter/internal/provider"
)

const (
	profileURL     = "https://api.anthropic.com/api/oauth/profile"
	profileTimeout = 5 * time.Second
)

// profileEndpoint is a variable so tests can point it at a local server.
var profileEndpoint = profileURL

// AccountIdentity reads the email of the account this source's token belongs
// to from Anthropic's profile endpoint. An expired token is not renewed: the
// identity is then unknown until the next login refresh.
func (p *Provider) AccountIdentity(ctx context.Context) (provider.AccountIdentity, error) {
	creds, err := p.readCredentials()
	if err != nil {
		return provider.AccountIdentity{}, fmt.Errorf("credentials: %w", err)
	}
	if creds.IsExpired() {
		return provider.AccountIdentity{}, errors.New("login expired")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileEndpoint, nil)
	if err != nil {
		return provider.AccountIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken())
	req.Header.Set("anthropic-beta", betaHeader)

	client := &http.Client{Timeout: profileTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return provider.AccountIdentity{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return provider.AccountIdentity{}, fmt.Errorf("profile: http %d", resp.StatusCode)
	}
	var profile struct {
		Account struct {
			Email string `json:"email"`
		} `json:"account"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&profile); err != nil {
		return provider.AccountIdentity{}, fmt.Errorf("parse profile: %w", err)
	}
	email := strings.TrimSpace(profile.Account.Email)
	if email == "" {
		return provider.AccountIdentity{}, errors.New("profile has no email")
	}
	return provider.AccountIdentity{Email: email}, nil
}
