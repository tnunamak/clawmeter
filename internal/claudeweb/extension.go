package claudeweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const extensionAddress = "127.0.0.1:17344"
const devExtensionID = "hfpaojbfhfofabcaappnobhbfddeckjm"

// ExtensionOrigins admits the fixed unpacked extension.
var ExtensionOrigins = []string{"chrome-extension:" + "//" + devExtensionID}

// ExtensionState records a browser sign-out without any account identity.
type ExtensionState struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

func extensionStatePath() (string, error) {
	dir, err := stateDir()
	return filepath.Join(dir, "claude-api-extension.json"), err
}

// ReadExtensionState returns the most recent sign-out, or an empty state.
func ReadExtensionState() (ExtensionState, error) {
	path, err := extensionStatePath()
	if err != nil {
		return ExtensionState{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ExtensionState{}, nil
	}
	if err != nil {
		return ExtensionState{}, err
	}
	var state ExtensionState
	if json.Unmarshal(data, &state) != nil || state.Status != "signed_out" || state.At.IsZero() {
		return ExtensionState{}, fmt.Errorf("invalid extension state")
	}
	return state, nil
}

func writeExtensionState(state ExtensionState) error {
	path, err := extensionStatePath()
	if err != nil {
		return err
	}
	if state.Status == "" {
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

// ServeExtension receives browser polls until ctx is cancelled.
func ServeExtension(ctx context.Context, accounts func() []Account, saved func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", extensionAddress)
	if err != nil {
		return fmt.Errorf("start Claude extension: %w", err)
	}
	server := &http.Server{
		Handler: extensionHandler(accounts, saved), ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stop, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if server.Shutdown(stop) != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type extensionLink struct {
	Organization  string `json:"organization"`
	Plan          string `json:"plan"`
	MonthlyCredit int64  `json:"monthly_credit"`
}

// Shadow the bookmark's nonce and link; nonce is not accepted on this route.
type extensionCreditsPayload struct {
	Pool          string             `json:"pool"`
	Name          string             `json:"name"`
	Currency      string             `json:"currency"`
	Balance       *int64             `json:"balance"`
	Grants        []grantTranche     `json:"grants"`
	MonthSpend    *int64             `json:"month_spend"`
	MonthResetsAt string             `json:"month_resets_at"`
	Daily         map[string]float64 `json:"daily"`
	Link          *extensionLink     `json:"link"`
}

func extensionHandler(accounts func() []Account, saved func()) http.Handler {
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != extensionAddress {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		if !admit(w, r, http.MethodPost, ExtensionOrigins...) {
			return
		}
		if r.URL.Path != "/v1/api-credits" && r.URL.Path != "/v1/status" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var trailing any
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if r.URL.Path == "/v1/status" {
			var payload struct {
				Status string `json:"status"`
			}
			if decoder.Decode(&payload) != nil || decoder.Decode(&trailing) != io.EOF || payload.Status != "signed_out" {
				http.Error(w, "invalid status", http.StatusBadRequest)
				return
			}
			if writeExtensionState(ExtensionState{Status: payload.Status, At: now}) != nil {
				http.Error(w, msgSaveFailed, http.StatusInternalServerError)
				return
			}
		} else {
			var payload extensionCreditsPayload
			if decoder.Decode(&payload) != nil || decoder.Decode(&trailing) != io.EOF {
				http.Error(w, msgConsoleUnreadable, http.StatusBadRequest)
				return
			}
			shared := creditsPayload{
				Pool: payload.Pool, Name: payload.Name, Currency: payload.Currency,
				Balance: payload.Balance, Grants: payload.Grants, MonthSpend: payload.MonthSpend,
				MonthResetsAt: payload.MonthResetsAt, Daily: payload.Daily,
			}
			pool, err := shared.pool(now)
			if err != nil {
				http.Error(w, msgConsoleUnreadable, http.StatusBadRequest)
				return
			}
			pool.Source = "extension"
			if link := payload.Link; link != nil {
				pool.Plan, pool.MonthlyCredit = link.Plan, link.MonthlyCredit
				var matches []Account
				if accounts != nil && link.Organization != "" {
					for _, account := range accounts() {
						if account.OrgUUID == link.Organization && validResetCreditsTarget(account.Key) {
							matches = append(matches, account)
						}
					}
				}
				if len(matches) == 1 {
					salt, err := randomHex()
					if err != nil {
						http.Error(w, msgSaveFailed, http.StatusInternalServerError)
						return
					}
					pool.LinkedTarget, pool.LinkSalt, pool.LinkHash = matches[0].Key, salt, AccountHash(salt, link.Organization)
				}
			}
			if pool.Validate() != nil {
				http.Error(w, msgConsoleUnreadable, http.StatusBadRequest)
				return
			}
			if WriteAPICreditPool(pool) != nil || writeExtensionState(ExtensionState{}) != nil {
				http.Error(w, msgSaveFailed, http.StatusInternalServerError)
				return
			}
		}
		if saved != nil {
			saved()
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
