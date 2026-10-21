// Package browserext installs the unpacked browser extension that reads
// Claude resets and API credits.
package browserext

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/tnunamak/clawmeter/extension"
)

// pairingFile holds the token that pairs the extension with this user's
// Clawmeter: the extension sends it and the receiver checks it. Only this
// user can read it.
const pairingFile = "pairing.json"

// Token is the pairing token in the installed folder, or "" when there is
// none.
func Token() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return readToken(filepath.Join(dir, pairingFile))
}

// readToken reads a pairing file; a missing or malformed one has no token.
func readToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var pairing struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &pairing) != nil || len(pairing.Token) != 64 {
		return "", nil
	}
	return pairing.Token, nil
}

// private reports whether path is a regular file only its owner can read.
func private(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode().Perm() == 0o600)
}

// Dir is where the unpacked extension lives: a fixed folder, so the browser
// keeps loading it after Clawmeter updates it.
func Dir() (string, error) {
	var base string
	switch runtime.GOOS {
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "Library", "Application Support")
	default:
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "share")
		}
	}
	if base == "" {
		return "", fmt.Errorf("find the user data directory")
	}
	return filepath.Join(base, "clawmeter", "extension"), nil
}

// Install writes the extension and its pairing token into Dir, replacing
// files that changed and removing ones the extension no longer has. It
// returns the folder.
func Install() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	// Keep the folder's token, so a browser already paired stays paired and
	// every Clawmeter process reads the same one. A copy others could read
	// is replaced.
	token := ""
	if path := filepath.Join(dir, pairingFile); private(path) {
		if token, err = readToken(path); err != nil {
			return "", fmt.Errorf("install the browser extension: %w", err)
		}
	}
	if token == "" {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", fmt.Errorf("install the browser extension: %w", err)
		}
		token = hex.EncodeToString(raw[:])
	}
	pairing, err := json.Marshal(struct {
		Token string `json:"token"`
	}{token})
	if err != nil {
		return "", err
	}
	return dir, installTo(dir, pairing)
}

// installMu serializes installs in this process: the tray refreshes the
// folder at start, and the menu item can be clicked again meanwhile.
var installMu sync.Mutex

// installTo builds the extension in a fresh folder beside dir and swaps it
// in, so nothing is written through a link or into a half-written folder,
// and a browser never loads a mix of versions. A folder that already holds
// exactly the extension is left alone.
func installTo(dir string, pairing []byte) error {
	installMu.Lock()
	defer installMu.Unlock()
	if installed(dir, pairing) {
		return nil
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("install the browser extension: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".extension-*")
	if err != nil {
		return fmt.Errorf("install the browser extension: %w", err)
	}
	defer os.RemoveAll(stage)
	err = fs.WalkDir(extension.Files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := extension.Files.ReadFile(name)
		if err != nil {
			return err
		}
		path := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	})
	if err == nil {
		err = os.WriteFile(filepath.Join(stage, pairingFile), pairing, 0o600)
	}
	if err != nil {
		return fmt.Errorf("install the browser extension: %w", err)
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return fmt.Errorf("install the browser extension: %w", err)
	}
	// Move the old folder aside (a link moves as a link), then swap.
	old := ""
	if _, err := os.Lstat(dir); err == nil {
		old = stage + ".old"
		if err := os.Rename(dir, old); err != nil {
			return fmt.Errorf("replace the browser extension: %w", err)
		}
	}
	if err := os.Rename(stage, dir); err != nil {
		if old != "" {
			_ = os.Rename(old, dir)
		}
		// Another install may have just finished the same work.
		if installed(dir, pairing) {
			return nil
		}
		return fmt.Errorf("replace the browser extension: %w", err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

// installed reports whether dir is a real folder holding exactly the
// extension's files and pairing, with no links and nothing else.
func installed(dir string, pairing []byte) bool {
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return false
	}
	path := filepath.Join(dir, pairingFile)
	if !private(path) {
		return false
	}
	if existing, err := os.ReadFile(path); err != nil || !bytes.Equal(existing, pairing) {
		return false
	}
	want := map[string]bool{pairingFile: true}
	same := fs.WalkDir(extension.Files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		want[filepath.FromSlash(name)] = true
		path := filepath.Join(dir, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fs.ErrNotExist
		}
		data, err := extension.Files.ReadFile(name)
		if err != nil {
			return err
		}
		existing, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(existing, data) {
			return fs.ErrNotExist
		}
		return nil
	}) == nil
	if !same {
		return false
	}
	extra := false
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			extra = true
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			extra = true
			return fs.SkipAll
		}
		if rel, _ := filepath.Rel(dir, path); !entry.IsDir() && !want[rel] {
			extra = true
			return fs.SkipAll
		}
		return nil
	})
	return !extra
}

// Steps are the plain-text instructions for loading the folder.
func Steps(dir string) []string {
	return []string{
		"Open your browser's extensions page: chrome://extensions (Brave: brave://extensions, Edge: edge://extensions).",
		"Turn on Developer mode.",
		"Click Load unpacked and choose " + dir,
		"Click the Clawmeter icon in the toolbar. It shows what to sign in to, and opens the claude.ai tab that resets are read through.",
	}
}

// SetupURL is the setup page, with the folder in the fragment so the page
// can show it. A fragment never leaves the browser.
func SetupURL(dir string) string {
	return SetupPage + "#dir=" + url.QueryEscape(dir)
}

// SetupPage walks through loading the extension, in one browser or for
// several Claude accounts. Its source is site/extension/index.html.
const SetupPage = "https://tnunamak.github.io/clawmeter/extension/"
