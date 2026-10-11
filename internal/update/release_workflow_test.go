package update

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseRequiresSignedChecksums(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/semantic-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct{ Name, Run, Uses string }
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.Permissions["id-token"] == "write" {
		t.Fatal("workflow-wide OIDC write permission")
	}
	for name, job := range workflow.Jobs {
		if name == "upload" {
			if job.Permissions["id-token"] != "write" {
				t.Error("signing job lacks OIDC permission")
			}
		} else if job.Permissions["id-token"] == "write" {
			t.Errorf("non-signing job %s can mint OIDC tokens", name)
		}
	}
	signing := ""
	for _, step := range workflow.Jobs["upload"].Steps {
		if strings.Contains(step.Run, "cosign sign-blob") {
			signing = step.Run
		}
		if strings.HasPrefix(step.Uses, "sigstore/") {
			sha := strings.Split(step.Uses, "@")[1]
			if len(sha) != 40 {
				t.Errorf("unpinned signing action: %s", step.Uses)
			}
		}
	}
	if !strings.Contains(signing, "--bundle") || !strings.Contains(signing, bundleName) {
		t.Error("checksum signing does not produce a bundle")
	}
	publish := ""
	for _, step := range workflow.Jobs["publish"].Steps {
		publish += step.Run
	}
	guard := "test -s uploaded/" + bundleName
	if i, j := strings.Index(publish, guard), strings.Index(publish, "--draft=false"); i < 0 || j < 0 || i >= j {
		t.Error("publication does not refuse a missing signature bundle")
	}
}

func TestPublishSignedChecksumGate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("release publication runs on Linux")
	}
	data, err := os.ReadFile("../../.github/workflows/semantic-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct{ Steps []struct{ Run string } } `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	script := ""
	for _, step := range workflow.Jobs["publish"].Steps {
		script += step.Run + "\n"
	}
	for _, scenario := range []string{"missing bundle", "tampered bundle", "valid bundle"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "artifacts"), 0700); err != nil {
				t.Fatal(err)
			}
			write := func(name, value string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("artifacts/binary", "fixture", 0600)
			write("artifacts/"+sumsName, fmt.Sprintf("%x  binary\n", sha256.Sum256([]byte("fixture"))), 0600)
			if scenario != "missing bundle" {
				write("artifacts/"+bundleName, "fixture bundle verified by signing job", 0600)
			}
			write("gh", `#!/bin/bash
set -e
case "$1 $2" in
  'release view')
    if [[ "$*" == *isDraft* ]]; then echo true
    else find artifacts -type f -printf '%f\n'; fi ;;
  'release download')
    mkdir uploaded
    cp artifacts/* uploaded/
    if [ "$SCENARIO" = 'tampered bundle' ]; then echo tampered > uploaded/SHA256SUMS.txt.sigstore.json; fi ;;
  'release edit') touch published ;;
  *) exit 99 ;;
esac
`, 0700)
			cmd := exec.Command("bash", "-e", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TAG=v2.0.0", "SCENARIO="+scenario)
			output, err := cmd.CombinedOutput()
			_, publishedErr := os.Stat(filepath.Join(dir, "published"))
			if scenario == "valid bundle" {
				if err != nil || publishedErr != nil {
					t.Fatalf("signed release blocked: %v %v\n%s", err, publishedErr, output)
				}
			} else if err == nil || !os.IsNotExist(publishedErr) {
				t.Fatalf("unsigned/tampered release published: %v %v\n%s", err, publishedErr, output)
			}
		})
	}
}
