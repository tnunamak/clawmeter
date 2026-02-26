package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repo       = "tnunamak/clawmeter"
	apiURL     = "https://api.github.com/repos/" + repo + "/releases/latest"
	httpTimeout = 15 * time.Second
)

type Release struct {
	Version string
	URL     string
}

type ghRelease struct {
	TagName string `json:"tag_name"`
}

// Check queries GitHub for the latest release and returns it if newer
// than currentVersion. Returns nil if already up to date.
func Check(currentVersion string) (*Release, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check update: GitHub API returned %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}

	if rel.TagName == "" || rel.TagName == currentVersion {
		return nil, nil
	}

	// Simple comparison: if tags differ and current isn't "dev", it's an update
	if currentVersion == "dev" {
		return nil, nil
	}

	url := fmt.Sprintf(
		"https://github.com/%s/releases/download/%s/clawmeter-%s-%s",
		repo, rel.TagName, runtime.GOOS, runtime.GOARCH,
	)

	return &Release{Version: rel.TagName, URL: url}, nil
}

// Apply downloads the binary from url, verifies it, and replaces the
// currently running executable. The caller should restart after Apply returns.
func Apply(url string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "clawmeter-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpBin := filepath.Join(tmpDir, "clawmeter")

	// Download
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(tmpBin)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return fmt.Errorf("write binary: %w", err)
	}
	f.Close()

	if err := os.Chmod(tmpBin, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	// macOS quarantine
	if runtime.GOOS == "darwin" {
		exec.Command("xattr", "-d", "com.apple.quarantine", tmpBin).Run()
	}

	// Verify: run "help" as a smoke test
	if err := exec.Command(tmpBin, "help").Run(); err != nil {
		return fmt.Errorf("verify binary: %w", err)
	}

	// Replace: rename new over old.
	// On some systems os.Rename fails across filesystems; fall back to copy.
	if err := os.Rename(tmpBin, exe); err != nil {
		if err := copyFile(tmpBin, exe); err != nil {
			return fmt.Errorf("replace binary: %w", err)
		}
	}

	return nil
}

// Restart launches a new tray process and returns. The caller should
// exit after calling this.
func Restart() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "tray")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(0755)
}

// StripV removes a leading "v" prefix for display: "v1.2.3" -> "1.2.3".
func StripV(version string) string {
	return strings.TrimPrefix(version, "v")
}
