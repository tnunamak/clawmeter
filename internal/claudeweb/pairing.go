package claudeweb

import (
	"crypto/subtle"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Every OS user's browser reaches the same loopback port, so the receiver
// pairs with the extension installed in its own user's folder: the folder
// holds a token only that user can read, and the extension sends it with
// each reading. An extension from before pairing sends none, and a running
// browser keeps that copy until it restarts, so tokenless readings are
// accepted until the first paired one arrives.
type pairing struct {
	Token    string `json:"token"`
	Required bool   `json:"required"`
}

var (
	pairingMu sync.Mutex
	// loaded keeps the pairing once read, so a cache directory cleared while
	// Clawmeter runs cannot change the token under the installed folder.
	loaded *pairing
)

func pairingPath() (string, error) {
	dir, err := stateDir()
	return filepath.Join(dir, "claude-extension-pairing.json"), err
}

// loadPairing returns the saved pairing, creating its token on first use.
// The caller holds pairingMu.
func loadPairing() (pairing, error) {
	if loaded != nil {
		return *loaded, nil
	}
	path, err := pairingPath()
	if err != nil {
		return pairing{}, err
	}
	var saved pairing
	if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &saved) != nil || len(saved.Token) != 64 {
		token, err := randomHex()
		if err != nil {
			return pairing{}, err
		}
		saved = pairing{Token: token}
		if err := savePairing(path, saved); err != nil {
			return pairing{}, err
		}
	}
	loaded = &saved
	return saved, nil
}

func savePairing(path string, saved pairing) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

// ExtensionToken is the token the extension folder gives the extension.
func ExtensionToken() (string, error) {
	pairingMu.Lock()
	defer pairingMu.Unlock()
	saved, err := loadPairing()
	return saved.Token, err
}

// checkPairing reports whether a reading that carries token comes from this
// user's extension. The first paired reading makes the token required.
func checkPairing(token string) (bool, error) {
	pairingMu.Lock()
	defer pairingMu.Unlock()
	saved, err := loadPairing()
	if err != nil {
		return false, err
	}
	if token == "" {
		return !saved.Required, nil
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(saved.Token)) != 1 {
		return false, nil
	}
	if !saved.Required {
		saved.Required = true
		path, err := pairingPath()
		if err != nil {
			return false, err
		}
		if err := savePairing(path, saved); err != nil {
			return false, err
		}
		loaded = &saved
	}
	return true, nil
}
