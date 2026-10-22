package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tnunamak/clawmeter/internal/provider"
)

// AccountIdentity reads the email, ChatGPT account and plan from the ID token
// Codex saved in auth.json with the access token. The file is Codex's own
// local record, so the token's signature is not checked.
func (p *Provider) AccountIdentity(context.Context) (provider.AccountIdentity, error) {
	auth, err := readAuthFile(p.authDirectory())
	if err != nil {
		return provider.AccountIdentity{}, err
	}
	if auth.Tokens == nil || strings.TrimSpace(auth.Tokens.IDToken) == "" {
		return provider.AccountIdentity{}, errors.New("auth.json has no ChatGPT login")
	}
	claims, err := parseIDToken(auth.Tokens.IDToken)
	if err != nil {
		return provider.AccountIdentity{}, err
	}
	email := strings.TrimSpace(claims.Email)
	if email == "" {
		return provider.AccountIdentity{}, errors.New("ID token has no email")
	}
	workspace := strings.TrimSpace(claims.Auth.ChatGPTAccountID)
	saved := strings.TrimSpace(auth.Tokens.AccountID)
	switch {
	case workspace == "":
		workspace = saved
	case saved != "" && saved != workspace:
		return provider.AccountIdentity{}, errors.New("ID token and saved account disagree")
	}
	return provider.AccountIdentity{
		Email:       email,
		WorkspaceID: workspace,
		PlanType:    strings.TrimSpace(claims.Auth.ChatGPTPlanType),
	}, nil
}

type idTokenClaims struct {
	Email string `json:"email"`
	Auth  struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		ChatGPTPlanType  string `json:"chatgpt_plan_type"`
	} `json:"https://api.openai.com/auth"`
}

func parseIDToken(token string) (idTokenClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return idTokenClaims{}, errors.New("ID token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return idTokenClaims{}, errors.New("ID token payload is not base64url")
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return idTokenClaims{}, errors.New("ID token payload is not JSON")
	}
	return claims, nil
}
