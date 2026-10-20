// Package browserext installs the unpacked browser extension that reads
// Claude resets and API credits, and writes a page that walks through
// loading it.
package browserext

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tnunamak/clawmeter/extension"
)

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

// Install writes the extension into Dir, replacing files that changed and
// removing ones the extension no longer has. It returns the folder.
func Install() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return dir, installTo(dir)
}

func installTo(dir string) error {
	want := map[string]bool{}
	err := fs.WalkDir(extension.Files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		want[filepath.FromSlash(name)] = true
		data, err := extension.Files.ReadFile(name)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	})
	if err != nil {
		return fmt.Errorf("install the browser extension: %w", err)
	}
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if rel, err := filepath.Rel(dir, path); err == nil && !want[rel] {
			return os.Remove(path)
		}
		return nil
	})
}

// Steps are the plain-text instructions for loading the folder.
func Steps(dir string) []string {
	return []string{
		"Open your browser's extensions page: chrome://extensions (Brave: brave://extensions, Edge: edge://extensions).",
		"Turn on Developer mode.",
		"Click Load unpacked and choose " + dir,
		"Keep a claude.ai tab open; Clawmeter reads resets through it.",
	}
}

// WriteGuide writes a page next to the extension folder that walks through
// loading it, and returns the page's path.
func WriteGuide(dir string) (string, error) {
	var page bytes.Buffer
	if err := guide.Execute(&page, struct{ Dir string }{dir}); err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(dir), "extension-setup.html")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write the extension setup page: %w", err)
	}
	if err := os.WriteFile(path, page.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write the extension setup page: %w", err)
	}
	return path, nil
}

var guide = template.Must(template.New("guide").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Connect Clawmeter to your browser</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:42rem;margin:3rem auto;padding:0 1rem;color:#1d1d1f;background:#fff}
@media (prefers-color-scheme:dark){body{color:#e8e8e8;background:#1c1c1e}code{background:#2c2c2e}}
h1{font-size:1.5rem;margin-bottom:.25rem}p.lead{color:#6e6e73;margin-top:0}
ol{padding-left:1.25rem}li{margin:.75rem 0}
code{background:#f2f2f7;padding:.1rem .35rem;border-radius:4px;word-break:break-all}
button{margin-left:.5rem;font:inherit;font-size:.85rem;padding:.1rem .6rem;border-radius:6px;border:1px solid #8e8e93;background:transparent;color:inherit;cursor:pointer}
.note{color:#6e6e73;font-size:.9rem}
</style></head><body>
<h1>Connect Clawmeter to your browser</h1>
<p class="lead">claude.ai and the Claude Console have no API for your reset credits and prepaid API credit balance. A small browser extension reads them from your signed-in session and sends them to Clawmeter on this computer only.</p>
<ol>
<li>Open your browser's extensions page: <code>chrome://extensions</code><button data-copy="chrome://extensions">Copy</button><br>
<span class="note">Brave: <code>brave://extensions</code> · Edge: <code>edge://extensions</code>. Browsers don't let a page link there, so paste it into the address bar.</span></li>
<li>Turn on <b>Developer mode</b>.</li>
<li>Click <b>Load unpacked</b> and choose this folder:<br><code id="dir">{{.Dir}}</code><button data-copy="{{.Dir}}">Copy</button></li>
<li>Keep a claude.ai tab open. Clawmeter reads resets through it, and API credits through a Claude Console tab or your signed-in session.</li>
</ol>
<p class="note">Clawmeter keeps this folder up to date. After an update, click the reload arrow on the extension's card, or restart the browser. The extension only talks to claude.ai, the Claude Console and Clawmeter on 127.0.0.1. Firefox and Safari are not supported yet.</p>
<script>
for (const button of document.querySelectorAll("button[data-copy]")) {
  button.addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(button.dataset.copy); button.textContent = "Copied"; }
    catch { button.textContent = "Select and copy"; }
  });
}
</script>
</body></html>
`))
