package claudeweb

import (
	"crypto/subtle"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Every OS user's browser reaches the same loopback port, so the receiver
// pairs with the extension folder Clawmeter installed for its own user. The
// folder holds a token only that user can read; the extension sends it with
// each reading, and the receiver reads it from the same file, so the two
// cannot disagree. An extension from before pairing sends no token, and a
// running browser keeps that copy until it restarts, so tokenless readings
// are accepted until the first paired one arrives.

var pairingMu sync.Mutex

func pairedPath() (string, error) {
	dir, err := stateDir()
	return filepath.Join(dir, "claude-extension-paired.json"), err
}

// checkPairing reports whether a reading that carries sent comes from this
// user's extension, whose token token reads. The first paired reading makes
// a token required from then on.
func checkPairing(sent string, token func() (string, error)) (bool, error) {
	pairingMu.Lock()
	defer pairingMu.Unlock()
	path, err := pairedPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	paired := err == nil
	if sent == "" {
		return !paired, nil
	}
	expected, err := token()
	if err != nil {
		return false, err
	}
	if expected == "" || subtle.ConstantTimeCompare([]byte(sent), []byte(expected)) != 1 {
		return false, nil
	}
	if !paired {
		data, err := json.Marshal(struct {
			At time.Time `json:"at"`
		}{time.Now()})
		if err != nil {
			return false, err
		}
		if err := writePrivateFile(path, data); err != nil {
			return false, err
		}
	}
	return true, nil
}
