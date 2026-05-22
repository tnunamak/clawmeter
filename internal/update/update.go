package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repo            = "tnunamak/clawmeter"
	defaultAPIURL   = "https://api.github.com/repos/" + repo + "/releases/latest"
	defaultDLPrefix = "https://github.com/" + repo + "/releases/download"
	httpTimeout     = 15 * time.Second
	restartHelper   = "__restart-tray"
	restartDelay    = 750 * time.Millisecond
)

// apiURL and dlPrefix are overridable by tests via the package-level
// Check function below. They are not exported to keep the public API stable.
var (
	apiURL     = defaultAPIURL
	dlPrefix   = defaultDLPrefix
	httpClient = &http.Client{Timeout: httpTimeout}
)

type Release struct {
	Version string
	URL     string
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check queries GitHub for the latest release and returns it if newer
// than currentVersion. Returns nil if already up to date.
func Check(ctx context.Context, currentVersion string) (*Release, error) {
	return checkWith(ctx, currentVersion, apiURL, dlPrefix, httpClient)
}

func checkWith(ctx context.Context, currentVersion, api, dl string, client *http.Client) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", api, nil)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check update: GitHub API returned %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}

	if rel.TagName == "" || rel.TagName == currentVersion {
		return nil, nil
	}

	// Simple comparison: if tags differ and current isn't "dev", it's an update
	if currentVersion == "dev" {
		return nil, nil
	}

	assetName := assetNameFor(runtime.GOOS, runtime.GOARCH)
	url := ""
	for _, asset := range rel.Assets {
		if asset.Name == assetName && asset.URL != "" {
			url = asset.URL
			break
		}
	}
	if url == "" {
		if len(rel.Assets) > 0 {
			return nil, fmt.Errorf("check update: release %s has no asset %s", rel.TagName, assetName)
		}
		url = fmt.Sprintf("%s/%s/%s", strings.TrimRight(dl, "/"), rel.TagName, assetName)
	}

	return &Release{Version: rel.TagName, URL: url}, nil
}

func assetNameFor(goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("clawmeter-%s-%s%s", goos, goarch, ext)
}

// CleanupOld removes leftover .old files from a previous update (Windows).
func CleanupOld() {
	exe, err := ExecutablePath()
	if err != nil {
		return
	}
	os.Remove(exe + ".old")
}

func ExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	return exe, nil
}

// Apply downloads the binary from url, verifies it, and replaces the
// currently running executable. The caller should restart after Apply returns.
func Apply(ctx context.Context, url string) error {
	exe, err := ExecutablePath()
	if err != nil {
		return err
	}
	return ApplyTo(ctx, url, exe)
}

func ApplyTo(ctx context.Context, url, exe string) error {
	if exe == "" {
		return errors.New("executable path is empty")
	}
	tmpDir, err := os.MkdirTemp("", "clawmeter-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	binName := "clawmeter"
	if runtime.GOOS == "windows" {
		binName = "clawmeter.exe"
	}
	tmpBin := filepath.Join(tmpDir, binName)

	// Download
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
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

	// Replace the running binary.
	// On Windows, you can't delete or overwrite a running exe, but you CAN
	// rename it. So: rename current → .old, then move new into place.
	// On Linux/macOS, unlink works on a running binary.
	oldExe := exe + ".old"
	os.Remove(oldExe) // clean up any previous .old

	if runtime.GOOS == "windows" {
		// Rename running exe out of the way, then move new one in
		if err := os.Rename(exe, oldExe); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}
		if err := os.Rename(tmpBin, exe); err != nil {
			// Rollback
			os.Rename(oldExe, exe)
			return fmt.Errorf("replace binary: %w", err)
		}
		// .old can't be deleted while the old process runs; CleanupOld() handles it next launch
	} else {
		os.Remove(exe)
		if err := os.Rename(tmpBin, exe); err != nil {
			if err := copyFile(tmpBin, exe); err != nil {
				return fmt.Errorf("replace binary: %w", err)
			}
		}
	}

	return nil
}

// Restart launches a detached helper from the updated binary and returns.
// The helper starts the replacement tray after the current process exits, so
// the desktop tray watcher sees a clean unregister/register sequence.
func Restart(exe string) error {
	if exe == "" {
		return errors.New("executable path is empty")
	}
	cmd := exec.Command(exe, restartHelper, "--parent-pid", strconv.Itoa(os.Getpid()), "--exe", exe)
	detachRestartCommand(cmd)
	return cmd.Start()
}

// HandleRestartHelper runs the hidden restart helper command. It returns
// handled=false when args do not name the helper command.
func HandleRestartHelper(args []string) (handled bool, code int) {
	if len(args) == 0 || args[0] != restartHelper {
		return false, 0
	}
	parentPID, exe, err := parseRestartHelperArgs(args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: restart helper: %v\n", err)
		return true, 2
	}
	if exe == "" {
		exe, err = os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "clawmeter: restart helper: %v\n", err)
			return true, 1
		}
	}

	waitForRestartParent(parentPID, 5*time.Second)
	time.Sleep(250 * time.Millisecond)

	cmd := exec.Command(exe, "tray")
	detachRestartCommand(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "clawmeter: restart tray: %v\n", err)
		return true, 1
	}
	return true, 0
}

func parseRestartHelperArgs(args []string) (parentPID int, exe string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--parent-pid":
			i++
			if i >= len(args) {
				return 0, "", errors.New("--parent-pid requires a value")
			}
			parentPID, err = strconv.Atoi(args[i])
			if err != nil || parentPID < 0 {
				return 0, "", fmt.Errorf("invalid --parent-pid %q", args[i])
			}
		case "--exe":
			i++
			if i >= len(args) {
				return 0, "", errors.New("--exe requires a value")
			}
			exe = args[i]
		default:
			return 0, "", fmt.Errorf("unknown argument %q", args[i])
		}
	}
	return parentPID, exe, nil
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
