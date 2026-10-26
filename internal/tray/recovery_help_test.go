package tray

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestRecoveryHelpEscapesTextAndPreservesCommands(t *testing.T) {
	dir := t.TempDir()
	command := "printf '<script>&\"quoted\"</script>'\r\n# exact bytes\n"
	page := recoveryHelpPage{Title: "<script>alert(1)</script>", Account: "A & <B>", Description: "<img src=x onerror=alert(1)>", Commands: []string{command}, Links: []recoveryHelpLink{{Label: "<b>Console</b>", URL: "https://example.com/settings?a=1&b=2"}}}
	target, err := writeRecoveryHelp(dir, page)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "file" {
		t.Fatal(target, err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "recovery-help.html"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, bad := range []string{"<img src=x", "<b>Console</b>", "<title><script>"} {
		if strings.Contains(text, bad) {
			t.Fatal("unescaped", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "A &amp; &lt;B&gt;", "Copy command", "Copy unavailable", "Copied.", "https://example.com/settings?a=1&amp;b=2"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing", want)
		}
	}
	match := regexp.MustCompile(`data-command="([^"]+)"`).FindStringSubmatch(text)
	if len(match) != 2 {
		t.Fatal("missing encoded command")
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil || string(decoded) != command {
		t.Fatalf("command changed %q %v", decoded, err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{dir, filepath.Join(dir, "recovery-help.html")} {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			want := os.FileMode(0600)
			if p == dir {
				want = 0700
			}
			if info.Mode().Perm() != want {
				t.Fatal(p, info.Mode())
			}
		}
	}
}

func TestRecoveryHelpRejectsHostileURLsBeforeWriting(t *testing.T) {
	for _, target := range []string{"javascript:alert(1)", "file:///tmp/private", "http://example.com", "https:///missing", "https://user:password@example.com", "https://example.com/\n", "https://example.com/%0a", "https://-bad.example", "https://example..com", "https://."} {
		dir := t.TempDir()
		_, err := writeRecoveryHelp(dir, recoveryHelpPage{Links: []recoveryHelpLink{{Label: "link", URL: target}}})
		if err == nil {
			t.Fatal("accepted", target)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatal("invalid URL wrote files")
		}
	}
}

func TestRecoveryHelpStableAtomicReplacementAndConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := writeRecoveryHelp(dir, recoveryHelpPage{Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target, err := writeRecoveryHelp(dir, recoveryHelpPage{Title: "replacement", Commands: []string{"echo harmless"}})
			if err != nil {
				failures <- err
			} else if target != first {
				t.Error("unstable file URL")
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "recovery-help.html"))
	if err != nil || !strings.Contains(string(body), "<title>replacement</title>") || !strings.HasSuffix(string(body), "</html>\n") {
		t.Fatal("incomplete final document", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatal("temporary file buildup", entries)
	}
	kept, _ := os.ReadFile(other)
	if string(kept) != "keep" {
		t.Fatal("unrelated content changed")
	}
}

func TestRecoveryHelpRejectsSymlinksAndNonregularTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation permissions vary on Windows")
	}
	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRecoveryHelp(link, recoveryHelpPage{}); err == nil {
		t.Fatal("directory link accepted")
	}
	outside := filepath.Join(parent, "outside")
	if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "recovery-help.html")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRecoveryHelp(real, recoveryHelpPage{}); err == nil {
		t.Fatal("file link accepted")
	}
	body, _ := os.ReadFile(outside)
	if string(body) != "unchanged" {
		t.Fatal("wrote through link")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRecoveryHelp(real, recoveryHelpPage{}); err == nil {
		t.Fatal("directory target accepted")
	}
	entries, _ := os.ReadDir(real)
	if len(entries) != 1 {
		t.Fatal("left temporary files")
	}
}

func TestRecoveryHelpFileURLEscapingAndInvalidText(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "help space & unicode")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	target, err := writeRecoveryHelp(dir, recoveryHelpPage{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(target)
	wantPath := filepath.ToSlash(filepath.Join(dir, "recovery-help.html"))
	if !strings.HasPrefix(wantPath, "/") {
		wantPath = "/" + wantPath
	}
	if err != nil || u.Scheme != "file" || u.Path != wantPath {
		t.Fatal(target, err)
	}
	if _, err := writeRecoveryHelp(dir, recoveryHelpPage{Commands: []string{string([]byte{255})}}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := writeRecoveryHelp(filepath.Join(parent, "absent"), recoveryHelpPage{}); err == nil {
		t.Fatal("missing caller directory accepted")
	}
}

func TestRecoveryHelpAllowsSymlinkedAncestors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation permissions vary")
	}
	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	if err := os.MkdirAll(filepath.Join(real, "help"), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "linked")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRecoveryHelp(filepath.Join(linked, "help"), recoveryHelpPage{Title: "Linked cache"}); err != nil {
		t.Fatal(err)
	}
}

// Set CLAWMETER_HELP_TEST_DIR to render a synthetic page for browser checks.
func TestRecoveryHelpBrowserFixture(t *testing.T) {
	dir := os.Getenv("CLAWMETER_HELP_TEST_DIR")
	if dir == "" {
		t.Skip("set CLAWMETER_HELP_TEST_DIR to render the browser fixture")
	}
	_, err := writeRecoveryHelp(dir, recoveryHelpPage{
		Title: "Sign in to Claude", Account: "Claude · Work",
		Description: "Run this command in a terminal for this source, then choose Refresh Now in Clawmeter. A browser sign-in alone does not renew this tool's login.",
		Commands:    []string{"CLAUDE_CONFIG_DIR='/profiles/Work account' claude"},
		Links:       []recoveryHelpLink{{Label: "Open Claude Usage", URL: "https://claude.ai/settings/usage"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}
