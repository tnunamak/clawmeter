package claudeweb

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// ConsoleURL is the Claude Console page where the bookmark reads API credits.
const ConsoleURL = "https://platform.claude.com/settings/billing"

const consoleOrigin = "https://platform.claude.com"

// APICreditPool is a saved Claude Console snapshot of one organization's
// prepaid API credits. Pool is a hash of the organization ID; the raw ID is
// never stored. Amounts are minor units of Currency; DailySpend values are
// fractional minor units keyed by UTC date, as the Console reports them.
type APICreditPool struct {
	Pool          string             `json:"pool"`
	Name          string             `json:"name"`
	Currency      string             `json:"currency"`
	Balance       int64              `json:"balance"`
	Grants        []APICreditGrant   `json:"grants,omitempty"`
	MonthSpend    int64              `json:"month_spend"`
	MonthResetsAt time.Time          `json:"month_resets_at"`
	DailySpend    map[string]float64 `json:"daily_spend,omitempty"`
	ObservedAt    time.Time          `json:"observed_at"`
}

// APICreditGrant is one credit tranche: a monthly plan credit, a promotion,
// or a purchase. ExpiresAt is zero when the Console reports no expiry.
type APICreditGrant struct {
	Name      string    `json:"name"`
	Granted   int64     `json:"granted"`
	Remaining int64     `json:"remaining"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

var (
	poolIDPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	currencyFormat = regexp.MustCompile(`^[A-Z]{3}$`)
	dayFormat      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

const (
	maxGrants    = 50
	maxSpendDays = 62
	maxMinor     = 1_000_000_000_00 // one billion in major units
)

// Validate rejects a snapshot Clawmeter can't trust, so a changed Console
// format never shows as a wrong balance.
func (p APICreditPool) Validate() error {
	if !poolIDPattern.MatchString(p.Pool) {
		return fmt.Errorf("invalid pool")
	}
	if !validPoolName(p.Name) {
		return fmt.Errorf("invalid organization name")
	}
	if !currencyFormat.MatchString(p.Currency) {
		return fmt.Errorf("invalid currency")
	}
	if p.Balance < 0 || p.Balance > maxMinor || p.MonthSpend < 0 || p.MonthSpend > maxMinor {
		return fmt.Errorf("invalid amount")
	}
	if p.ObservedAt.IsZero() || p.MonthResetsAt.IsZero() {
		return fmt.Errorf("missing time")
	}
	if len(p.Grants) > maxGrants {
		return fmt.Errorf("too many grants")
	}
	for _, grant := range p.Grants {
		if !validPoolName(grant.Name) || grant.Granted < 0 || grant.Granted > maxMinor || grant.Remaining < 0 || grant.Remaining > grant.Granted || grant.GrantedAt.IsZero() {
			return fmt.Errorf("invalid grant")
		}
	}
	if len(p.DailySpend) > maxSpendDays {
		return fmt.Errorf("too many spend days")
	}
	for day, amount := range p.DailySpend {
		if !dayFormat.MatchString(day) || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > maxMinor {
			return fmt.Errorf("invalid daily spend")
		}
		if _, err := time.Parse(time.DateOnly, day); err != nil {
			return fmt.Errorf("invalid daily spend")
		}
	}
	return nil
}

func validPoolName(name string) bool {
	if name == "" || len(name) > 120 || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// WriteAPICreditPool saves a pool snapshot under its pool hash.
func WriteAPICreditPool(pool APICreditPool) error {
	if err := pool.Validate(); err != nil {
		return err
	}
	sort.Slice(pool.Grants, func(i, j int) bool { return pool.Grants[i].GrantedAt.Before(pool.Grants[j].GrantedAt) })
	data, err := json.Marshal(pool)
	if err != nil {
		return fmt.Errorf("encode API credits: %w", err)
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, apiCreditFilePrefix+pool.Pool[:16]+".json"), data)
}

const apiCreditFilePrefix = "claude-api-credits-"

// ReadAPICreditPools returns every saved pool, ordered by name. Unreadable
// or invalid files are skipped.
func ReadAPICreditPools() ([]APICreditPool, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, apiCreditFilePrefix+"*.json"))
	if err != nil {
		return nil, err
	}
	pools := make([]APICreditPool, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var pool APICreditPool
		if json.Unmarshal(data, &pool) != nil || pool.Validate() != nil || filepath.Base(path) != apiCreditFilePrefix+pool.Pool[:16]+".json" {
			continue
		}
		pools = append(pools, pool)
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	return pools, nil
}

type creditsPayload struct {
	Nonce         string             `json:"nonce"`
	Pool          string             `json:"pool"`
	Name          string             `json:"name"`
	Currency      string             `json:"currency"`
	Balance       *int64             `json:"balance"`
	Grants        []grantTranche     `json:"grants"`
	MonthSpend    *int64             `json:"month_spend"`
	MonthResetsAt string             `json:"month_resets_at"`
	Daily         map[string]float64 `json:"daily"`
}

type grantTranche struct {
	Name      string  `json:"name"`
	Granted   int64   `json:"granted"`
	Remaining int64   `json:"remaining"`
	GrantedAt string  `json:"granted_at"`
	ExpiresAt *string `json:"expires_at"`
}

const msgConsoleUnreadable = "Claude Console sent credit data Clawmeter can't read. Nothing was saved."

// handleCredits saves the Console's API credit snapshot for one organization.
func (s *Session) handleCredits(w http.ResponseWriter, r *http.Request) {
	if !admit(w, r, http.MethodPost, consoleOrigin) {
		return
	}
	if s.kind != KindAPICredits {
		s.reply(w, http.StatusConflict, msgWantUsage, true)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer r.Body.Close()
	var payload creditsPayload
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var trailing any
	if decoder.Decode(&payload) != nil || decoder.Decode(&trailing) != io.EOF || payload.Balance == nil || payload.MonthSpend == nil || payload.Grants == nil || payload.Daily == nil {
		s.reply(w, http.StatusBadRequest, msgOutdated, true)
		return
	}
	if payload.Nonce != s.nonce {
		s.reply(w, http.StatusBadRequest, msgEnded, false)
		return
	}
	pool, err := payload.pool(time.Now())
	if err != nil {
		s.reply(w, http.StatusBadRequest, msgConsoleUnreadable, true)
		return
	}
	found := "Saved API credits for " + pool.Name + "."
	s.save(w, Result{APICredits: pool}, func() error { return WriteAPICreditPool(pool) }, found)
}

func (p creditsPayload) pool(now time.Time) (APICreditPool, error) {
	monthResetsAt, err := time.Parse(time.RFC3339Nano, p.MonthResetsAt)
	if err != nil {
		return APICreditPool{}, fmt.Errorf("invalid month reset")
	}
	pool := APICreditPool{
		Pool: p.Pool, Name: p.Name, Currency: p.Currency, Balance: *p.Balance,
		MonthSpend: *p.MonthSpend, MonthResetsAt: monthResetsAt, DailySpend: p.Daily, ObservedAt: now,
	}
	if len(p.Grants) > maxGrants {
		return APICreditPool{}, fmt.Errorf("too many grants")
	}
	for _, tranche := range p.Grants {
		grantedAt, err := time.Parse(time.RFC3339Nano, tranche.GrantedAt)
		if err != nil {
			return APICreditPool{}, fmt.Errorf("invalid grant time")
		}
		grant := APICreditGrant{Name: tranche.Name, Granted: tranche.Granted, Remaining: tranche.Remaining, GrantedAt: grantedAt}
		if tranche.ExpiresAt != nil {
			if grant.ExpiresAt, err = time.Parse(time.RFC3339Nano, *tranche.ExpiresAt); err != nil {
				return APICreditPool{}, fmt.Errorf("invalid grant expiry")
			}
		}
		pool.Grants = append(pool.Grants, grant)
	}
	return pool, pool.Validate()
}
