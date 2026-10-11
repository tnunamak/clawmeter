package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const sumsName = "SHA256SUMS.txt"

func validateDownloadURL(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return errors.New("download requires HTTPS without userinfo on port 443")
	}
	// GitHub's documented release CDN hosts; do not allow arbitrary subdomains.
	// https://docs.github.com/en/actions/reference/runners/self-hosted-runners#accessible-domains-by-function
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com", "release-assets.githubusercontent.com":
		return nil
	default:
		return fmt.Errorf("untrusted download host %q", u.Hostname())
	}
}

func artifactURLs(raw string) (asset, sums, tag string, err error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", err
	}
	if err := validateDownloadURL(u); err != nil {
		return "", "", "", err
	}
	prefix := "/" + repo + "/releases/download/"
	if strings.ToLower(u.Hostname()) != "github.com" || !strings.HasPrefix(u.Path, prefix) || u.RawQuery != "" || u.Fragment != "" {
		return "", "", "", errors.New("expected a canonical clawmeter GitHub release asset URL")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")
	if len(parts) != 2 || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
		return "", "", "", errors.New("invalid release asset path")
	}
	if _, err := parseVersion(parts[0]); err != nil {
		return "", "", "", err
	}
	asset = parts[1]
	u.Path = prefix + parts[0] + "/" + sumsName
	u.RawPath = ""
	return asset, u.String(), parts[0], nil
}

func download(ctx context.Context, raw string) (*http.Response, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := validateDownloadURL(u); err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many download redirects")
			}
			return validateDownloadURL(req.URL)
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func downloadSums(ctx context.Context, raw string) ([]byte, error) {
	resp, err := download(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	const maxSums = 1 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSums+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSums {
		return nil, errors.New("checksum file too large")
	}
	return data, nil
}

// verifyArtifact is the integrity gate, before chmod, execution, or replacement.
// A future release can add signature verification here.
func verifyArtifact(filename, asset string, sums []byte) error {
	var expected string
	matches := 0
	scanner := bufio.NewScanner(strings.NewReader(string(sums)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == asset {
			if len(fields) != 2 {
				return errors.New("checksums: malformed entry")
			}
			matches++
			expected = fields[0]
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	if matches != 1 {
		return fmt.Errorf("checksums: expected exactly one entry for %s, got %d", asset, matches)
	}
	digest, err := hex.DecodeString(expected)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("checksums: invalid SHA-256")
	}
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected) {
		return errors.New("checksums: SHA-256 mismatch")
	}
	return nil
}
