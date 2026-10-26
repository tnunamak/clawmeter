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
	// defaultLatestURL redirects to the latest release's tag page.
	defaultLatestURL = "https://github.com/" + repo + "/releases/latest"
	httpTimeout      = 15 * time.Second
	restartHelper    = "__restart-tray"
	restartDelay     = 750 * time.Millisecond
)

// apiURL and dlPrefix are overridable by tests via the package-level
// Check function below. They are not exported to keep the public API stable.
var (
	apiURL       = defaultAPIURL
	dlPrefix     = defaultDLPrefix
	latestURL    = defaultLatestURL
	httpClient   = &http.Client{Timeout: httpTimeout}
	osExecutable = os.Executable
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

	var rel ghRelease
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
			return nil, fmt.Errorf("check update: %w", err)
		}
	case http.StatusForbidden, http.StatusTooManyRequests:
		// Anonymous API requests share a small hourly limit per address, so
		// other programs on the network can use it up. The release page is
		// not under that limit; it names the tag but not the assets.
		tag, err := latestTag(ctx, latestURL, client)
		if err != nil {
			return nil, fmt.Errorf("check update: GitHub API returned %d; %w", resp.StatusCode, err)
		}
		rel.TagName = tag
	default:
		return nil, fmt.Errorf("check update: GitHub API returned %d", resp.StatusCode)
	}

	if rel.TagName == "" || rel.TagName == currentVersion {
		return nil, nil
	}

	// Development builds do not auto-update.
	if currentVersion == "dev" {
		return nil, nil
	}

	newer, err := newerVersion(currentVersion, rel.TagName)
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	if !newer {
		return nil, nil
	}

	// The download URL is always built from the checked tag, never taken from
	// the release metadata, so a newer tag cannot point at an older asset.
	assetName := assetNameFor(runtime.GOOS, runtime.GOARCH)
	if len(rel.Assets) > 0 {
		found := false
		for _, asset := range rel.Assets {
			found = found || asset.Name == assetName
		}
		if !found {
			return nil, fmt.Errorf("check update: release %s has no asset %s", rel.TagName, assetName)
		}
	}
	url := fmt.Sprintf("%s/%s/%s", strings.TrimRight(dl, "/"), rel.TagName, assetName)
	return &Release{Version: rel.TagName, URL: url}, nil
}

// latestTag reads the latest release's tag from where page redirects.
func latestTag(ctx context.Context, page string, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, page, nil)
	if err != nil {
		return "", err
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	location, err := resp.Location()
	if err != nil || resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("the release page returned %d without a redirect", resp.StatusCode)
	}
	if location.Host != req.URL.Host {
		return "", fmt.Errorf("the release page redirected to another host, %s", location.Host)
	}
	tag, found := strings.CutPrefix(location.Path, "/"+repo+"/releases/tag/")
	if !found {
		return "", fmt.Errorf("the release page redirected to %s", location.Path)
	}
	if _, err := parseVersion(tag); err != nil {
		return "", fmt.Errorf("the release page named tag %q: %w", tag, err)
	}
	return tag, nil
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
	if err != nil || checkHomebrewOrigin(exe) != nil {
		return
	}
	os.Remove(exe + ".old")
}

func ExecutablePath() (string, error) {
	exe, err := osExecutable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	return exe, nil
}

// ErrHomebrewManaged prevents replacing a binary owned by Homebrew.
var ErrHomebrewManaged = errors.New("this installation is managed by Homebrew; run `brew upgrade clawmeter` instead")

// CheckSelfUpdateAllowed checks the running executable before release checks or
// update effects. PATH and the operating system do not establish package origin.
func CheckSelfUpdateAllowed() error {
	exe, err := ExecutablePath()
	if err != nil {
		return err
	}
	return checkHomebrewOrigin(exe)
}

// checkHomebrewOrigin requires the resolved package layout AND a Homebrew
// receipt naming the clawmeter formula. A copied binary outside its keg is
// standalone, even when brew is installed. This is package evidence, not a
// security boundary against someone able to forge the installation's files.
func checkHomebrewOrigin(exe string) error {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// ApplyTo also supports a new destination. Preserve that behavior, but
		// do not treat a dangling symlink or a symlink loop as standalone.
		if _, statErr := os.Lstat(exe); !os.IsNotExist(statErr) {
			return fmt.Errorf("resolve update destination: %w", err)
		}
		parent, parentErr := filepath.EvalSymlinks(filepath.Dir(exe))
		if parentErr != nil {
			return fmt.Errorf("resolve update destination: %w", parentErr)
		}
		resolved = filepath.Join(parent, filepath.Base(exe))
	}
	bin := filepath.Dir(resolved)
	keg := filepath.Dir(bin)
	formula := filepath.Dir(keg)
	if filepath.Base(resolved) != "clawmeter" || filepath.Base(bin) != "bin" ||
		filepath.Base(formula) != "clawmeter" || filepath.Base(filepath.Dir(formula)) != "Cellar" {
		return nil
	}

	receiptPath := filepath.Join(keg, "INSTALL_RECEIPT.json")
	info, err := os.Lstat(receiptPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Homebrew receipt: %w", err)
	}
	// Never follow a receipt symlink or open special files. Keep reads bounded.
	if !info.Mode().IsRegular() {
		return nil
	}
	const maxReceiptSize = 1 << 20
	if info.Size() > maxReceiptSize {
		return errors.New("Homebrew receipt exceeds size limit")
	}
	f, err := os.Open(receiptPath)
	if err != nil {
		return fmt.Errorf("read Homebrew receipt: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxReceiptSize+1))
	if err != nil {
		return fmt.Errorf("read Homebrew receipt: %w", err)
	}
	if len(data) > maxReceiptSize {
		return errors.New("Homebrew receipt exceeds size limit")
	}
	var receipt struct {
		HomebrewVersion string `json:"homebrew_version"`
		Source          struct {
			Path string `json:"path"`
		} `json:"source"`
	}
	if json.Unmarshal(data, &receipt) != nil || receipt.HomebrewVersion == "" ||
		filepath.Base(receipt.Source.Path) != "clawmeter.rb" {
		return nil
	}
	return ErrHomebrewManaged
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

var (
	runSmoke = func(ctx context.Context, filename string) error {
		cmd := exec.CommandContext(ctx, filename, "help")
		cmd.WaitDelay = time.Second
		return cmd.Run()
	}
	renameFile   = os.Rename
	copyArtifact = io.Copy
	// installedVersion asks the binary at exe for its version. It runs only
	// the already-installed binary, never a download.
	installedVersion = func(ctx context.Context, exe string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "--version")
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		for _, field := range strings.Fields(string(out)) {
			if _, err := parseVersion(field); err == nil {
				return field, nil
			}
		}
		return "", errors.New("no version in output")
	}
)

// ErrNotNewer means the installed binary is already at or past the release:
// another updater ran after this release was checked.
var ErrNotNewer = errors.New("installed version is already up to date")

func ApplyTo(ctx context.Context, rawURL, exe string) error {
	if exe == "" {
		return errors.New("executable path is empty")
	}
	if err := checkHomebrewOrigin(exe); err != nil {
		return err
	}
	asset, sumsURL, tag, err := artifactURLs(rawURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	sums, err := downloadSums(ctx, sumsURL)
	if err != nil {
		return err
	}
	sig, err := downloadSig(ctx, sumsURL+sigSuffix)
	if err != nil {
		return err
	}
	if err := VerifySums(sums, sig, tag); err != nil {
		return err
	}

	// Stage on the destination filesystem so Unix replacement is one rename.
	pattern := ".clawmeter-update-*"
	if runtime.GOOS == "windows" {
		pattern += ".exe"
	}
	f, err := os.CreateTemp(filepath.Dir(exe), pattern)
	if err != nil {
		return fmt.Errorf("create staged binary: %w", err)
	}
	tmpBin := f.Name()
	defer os.Remove(tmpBin)
	defer f.Close()

	resp, err := download(ctx, rawURL)
	if err != nil {
		return err
	}
	_, copyErr := copyArtifact(f, resp.Body)
	resp.Body.Close()
	if copyErr != nil {
		return fmt.Errorf("write binary: %w", copyErr)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync binary: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close binary: %w", err)
	}
	if err := verifyArtifact(tmpBin, asset, sums); err != nil {
		return fmt.Errorf("verify artifact: %w", err)
	}
	if err := os.Chmod(tmpBin, 0755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	smokeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := runSmoke(smokeCtx, tmpBin); err != nil {
		return fmt.Errorf("verify binary: %w", err)
	}
	if err := smokeCtx.Err(); err != nil {
		return fmt.Errorf("verify binary: %w", err)
	}

	// The release was checked against the running version; another updater
	// may have installed something newer since. An unreadable version keeps
	// the old behaviour.
	if installed, err := installedVersion(ctx, exe); err == nil {
		if newer, err := newerVersion(installed, tag); err == nil && !newer {
			return ErrNotNewer
		}
	}

	if runtime.GOOS == "windows" {
		// Windows cannot overwrite a running executable; retain a rollback copy.
		oldExe := exe + ".old"
		if err := os.Remove(oldExe); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove previous binary: %w", err)
		}
		if err := renameFile(exe, oldExe); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}
		if err := renameFile(tmpBin, exe); err != nil {
			if rollbackErr := renameFile(oldExe, exe); rollbackErr != nil {
				return fmt.Errorf("replace binary: %v; restore %s: %w", err, oldExe, rollbackErr)
			}
			return fmt.Errorf("replace binary: %w", err)
		}
	} else if err := renameFile(tmpBin, exe); err != nil {
		return fmt.Errorf("replace binary: %w", err)
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
