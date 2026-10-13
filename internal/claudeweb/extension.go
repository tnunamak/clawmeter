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

// ServeExtension receives browser polls until ctx is cancelled. saved reports
// each stored reading; a Result without resets is an API credit reading.
func ServeExtension(ctx context.Context, accounts func() []Account, saved func(Result)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", extensionAddress)
	if err != nil {
		return fmt.Errorf("start Claude extension: %w", err)
	}
	server := &http.Server{
		Handler: ExtensionHandler(accounts, saved), ReadHeaderTimeout: 2 * time.Second,
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

type extensionResetsPayload struct {
	ObservedAt   string         `json:"observed_at"`
	Organization string         `json:"organization"`
	Grants       []grantPayload `json:"grants"`
}

// ExtensionHandler serves the extension's readings. It admits only the
// extension's origin on the loopback address; saved is as for ServeExtension.
func ExtensionHandler(accounts func() []Account, saved func(Result)) http.Handler {
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != extensionAddress {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		if !admit(w, r, http.MethodPost, ExtensionOrigins...) {
			return
		}
		if r.URL.Path != "/v1/api-credits" && r.URL.Path != "/v1/resets" && r.URL.Path != "/v1/status" {
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
		var result Result
		if r.URL.Path == "/v1/resets" {
			var payload extensionResetsPayload
			if decoder.Decode(&payload) != nil || decoder.Decode(&trailing) != io.EOF || payload.Organization == "" || payload.Grants == nil {
				http.Error(w, msgUnreadable, http.StatusBadRequest)
				return
			}
			observedAt, err := extensionObservedAt(payload.ObservedAt, now)
			if err != nil {
				http.Error(w, msgUnreadable, http.StatusBadRequest)
				return
			}
			// Which grants are usable is decided on this computer's clock; the
			// browser's time only dates the reading.
			credits, err := activeGrantExpiries(payload.Grants, now)
			if err != nil {
				http.Error(w, msgUnreadable, http.StatusBadRequest)
				return
			}
			// The browser may be signed in to organizations Clawmeter does
			// not track; those readings are dropped.
			var matches []Account
			if accounts != nil {
				for _, account := range accounts() {
					if account.OrgUUID == payload.Organization && validResetCreditsTarget(account.Key) {
						matches = append(matches, account)
					}
				}
			}
			if len(matches) != 1 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// A delayed reading, for example from another browser profile,
			// must not replace a newer one.
			if saved, err := ReadSummaryFor(matches[0].Key); err == nil && saved.ObservedAt.After(observedAt) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			salt, err := randomHex()
			if err != nil {
				http.Error(w, msgSaveFailed, http.StatusInternalServerError)
				return
			}
			summary := GrantSummary{Credits: credits, ObservedAt: observedAt, ResetCreditsTarget: matches[0].Key, MatchSalt: salt, MatchHash: AccountHash(salt, payload.Organization)}
			if WriteSummary(summary) != nil {
				http.Error(w, msgSaveFailed, http.StatusInternalServerError)
				return
			}
			result.Resets = summary
		} else if r.URL.Path == "/v1/status" {
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
			var payload creditsPayload
			if decoder.Decode(&payload) != nil || decoder.Decode(&trailing) != io.EOF {
				http.Error(w, msgConsoleUnreadable, http.StatusBadRequest)
				return
			}
			observedAt, err := extensionObservedAt(payload.ObservedAt, now)
			if err != nil {
				http.Error(w, msgConsoleUnreadable, http.StatusBadRequest)
				return
			}
			pool, err := payload.pool(observedAt)
			if err != nil {
				http.Error(w, msgConsoleUnreadable, http.StatusBadRequest)
				return
			}
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
			if WriteAPICreditPool(pool) != nil {
				http.Error(w, msgSaveFailed, http.StatusInternalServerError)
				return
			}
		}
		if saved != nil {
			saved(result)
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func extensionObservedAt(value string, now time.Time) (time.Time, error) {
	observed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || observed.IsZero() || observed.After(now.Add(time.Minute)) || observed.Before(now.Add(-5*time.Minute)) {
		return time.Time{}, fmt.Errorf("invalid observation time")
	}
	return observed, nil
}
