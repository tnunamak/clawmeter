package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tnunamak/clawmeter/internal/openaicredits"
	"github.com/tnunamak/clawmeter/internal/provider"
)

// The ChatGPT subscription benefit (API credits for Pro and Business) is read
// from the same ChatGPT backend as Codex quota, with the same local Codex
// login. These are the GETs the billing pages make; nothing here links an
// organization or claims a grant. The funded API balance is not readable this
// way: its endpoint wants a Platform browser session, so it still needs the
// browser extension.
const (
	productLinksPath      = "/backend-api/product-account-links"
	apiCreditBenefitPath  = "/benefits/chatgpt_api_credits_promo"
	apiCreditBenefitLimit = 20
	apiCreditTimeout      = 10 * time.Second
	maxBenefitCents       = 100_000_000_000
)

var (
	productLinksURL        = "https://chatgpt.com" + productLinksPath
	apiCreditHTTPClient    = newReadOnlyHTTPClient(apiCreditTimeout)
	openAIIDPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)
	usdDecimalPattern      = regexp.MustCompile(`^0*(\d{1,12})(?:\.(\d{1,40}))?$`)
	errNoChatGPTAuth       = errors.New("API credit benefit needs a ChatGPT login in Codex")
	errMalformedAPIBenefit = errors.New("unreadable API credit benefit response")
)

// HasChatGPTAuth reports whether this source holds a ChatGPT login that can
// read the API credit benefit. It reads only the local auth file.
func (p *Provider) HasChatGPTAuth() bool {
	auth, err := readAuthFile(p.authDirectory())
	if err != nil {
		return false
	}
	_, accountID, ok := resetCreditAuth(auth)
	return ok && openAIIDPattern.MatchString(accountID)
}

// ReadAPICreditBenefits returns the benefit as it applies to each API
// organization the ChatGPT account can link. Each result carries the opaque
// pool the browser extension uses for the same organization, never the raw
// organization ID.
func (p *Provider) ReadAPICreditBenefits(ctx context.Context) ([]provider.UsageCreditBenefit, error) {
	auth, err := readAuthFile(p.authDirectory())
	if err != nil {
		return nil, errNoChatGPTAuth
	}
	token, accountID, ok := resetCreditAuth(auth)
	if !ok || !openAIIDPattern.MatchString(accountID) {
		return nil, errNoChatGPTAuth
	}
	get := func(path string, query url.Values, into any) (time.Time, error) {
		return getProductLinks(ctx, token, accountID, path, query, into)
	}

	var discovery struct {
		Destinations []struct {
			Type        string `json:"destination_type"`
			AccountID   string `json:"destination_account_id"`
			DisplayName string `json:"display_name"`
		} `json:"destinations"`
	}
	discoveredAt, err := get("/discovery", url.Values{"account_id": {accountID}, "destination_type": {"api_org"}}, &discovery)
	if err != nil {
		return nil, err
	}
	if len(discovery.Destinations) > apiCreditBenefitLimit {
		return nil, errMalformedAPIBenefit
	}
	var offer benefitOffer
	offeredAt, err := get("/accounts/"+accountID+apiCreditBenefitPath, url.Values{"destination_type": {"api_org"}}, &offer)
	if err != nil {
		return nil, err
	}
	// The offer is bound to an organization only through discovery, so the
	// pair is only as fresh as the older of the two reads.
	observed := discoveredAt
	if offeredAt.Before(observed) {
		observed = offeredAt
	}
	base, err := offer.benefit(observed)
	if err != nil {
		return nil, err
	}

	var result []provider.UsageCreditBenefit
	seen := map[string]bool{}
	for _, d := range discovery.Destinations {
		if d.Type != "api_org" || !openAIIDPattern.MatchString(d.AccountID) || seen[d.AccountID] {
			continue
		}
		seen[d.AccountID] = true
		var link struct {
			Link    json.RawMessage `json:"link"`
			CanLink *bool           `json:"can_link"`
		}
		linkedAt, err := get("/organizations/"+d.AccountID, nil, &link)
		if err != nil {
			return nil, err
		}
		benefit := base.Clone()
		benefit.Pool = openaicredits.PoolID(d.AccountID)
		benefit.Name = organizationName(d.DisplayName)
		benefit.LinkState = linkState(link.Link, link.CanLink)
		benefit.LinkObservedAt = linkedAt
		result = append(result, benefit)
	}
	return result, nil
}

func getProductLinks(ctx context.Context, token, accountID, path string, query url.Values, into any) (time.Time, error) {
	reqCtx, cancel := context.WithTimeout(ctx, apiCreditTimeout)
	defer cancel()
	target := productLinksURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-ID", accountID)
	req.Header.Set("Originator", "Codex Desktop")
	req.Header.Set("OAI-Product-Sku", "CODEX")
	req.Header.Set("Accept", "application/json")
	resp, err := apiCreditHTTPClient.Do(req)
	if err != nil {
		// The URL holds the account and organization IDs; keep it out of
		// errors that reach logs and the cache.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return time.Time{}, fmt.Errorf("API credit benefit request: %w", err)
	}
	defer resp.Body.Close()
	if err := provider.RateLimitFromResponse(resp); err != nil {
		return time.Time{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return time.Time{}, fmt.Errorf("API credit benefit request: %w", &provider.HTTPError{StatusCode: resp.StatusCode})
	}
	at := time.Now()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return time.Time{}, fmt.Errorf("read API credit benefit response: %w", err)
	}
	if json.Unmarshal(body, into) != nil {
		return time.Time{}, errMalformedAPIBenefit
	}
	return at, nil
}

type benefitOffer struct {
	Eligible *bool `json:"eligible"`
	Offer    *struct {
		Currency  string `json:"currency"`
		Cadence   string `json:"cadence"`
		Period    string `json:"period_credit_amount_usd"`
		Claimable string `json:"claimable_credit_amount_usd"`
		ExpiresAt string `json:"expires_at"`
	} `json:"offer"`
}

// benefit applies the extension's rules (extension/openai.js): an eligible
// offer must be complete, or the whole reading is rejected.
func (o benefitOffer) benefit(observed time.Time) (provider.UsageCreditBenefit, error) {
	if o.Eligible == nil {
		return provider.UsageCreditBenefit{}, errMalformedAPIBenefit
	}
	b := provider.UsageCreditBenefit{Eligibility: "ineligible", LinkState: "unknown", ObservedAt: observed}
	if !*o.Eligible {
		return b, nil
	}
	offer := o.Offer
	if offer == nil || offer.Currency != "USD" || (offer.Cadence != "monthly" && offer.Cadence != "annual") {
		return b, errMalformedAPIBenefit
	}
	period, okPeriod := usdCents(offer.Period)
	claimable, okClaimable := usdCents(offer.Claimable)
	expires, err := time.Parse(time.RFC3339Nano, offer.ExpiresAt)
	if !okPeriod || !okClaimable || err != nil || period <= 0 || claimable > period {
		return b, errMalformedAPIBenefit
	}
	b.Eligibility, b.Currency, b.Cadence = "eligible", "USD", offer.Cadence
	b.PeriodCredit, b.Claimable, b.ExpiresAt = &period, &claimable, expires.UTC()
	return b, nil
}

// usdCents converts a non-negative decimal dollar string to cents, rounding
// half away from zero once, without passing through a float.
func usdCents(s string) (int64, bool) {
	m := usdDecimalPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	var cents int64
	for _, r := range m[1] {
		cents = cents*10 + int64(r-'0')
	}
	frac := m[2] + "000"
	cents = cents*100 + int64(frac[0]-'0')*10 + int64(frac[1]-'0')
	if frac[2] >= '5' {
		cents++
	}
	if cents > maxBenefitCents {
		return 0, false
	}
	return cents, true
}

// linkState follows the extension: a non-null link object is linked; a null
// link that can be created is unlinked; anything else is unknown.
func linkState(link json.RawMessage, canLink *bool) string {
	trimmed := strings.TrimSpace(string(link))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		return "linked"
	case trimmed == "null" && canLink != nil && *canLink:
		return "unlinked"
	default:
		return "unknown"
	}
}

func organizationName(s string) string {
	if strings.TrimSpace(s) != s || s == "" || utf8.RuneCountInString(s) > 120 || !utf8.ValidString(s) {
		return "OpenAI organization"
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "OpenAI organization"
		}
	}
	return s
}
