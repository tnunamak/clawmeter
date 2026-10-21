package extension

import (
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// Every file the manifest loads, every script beside it, and every icon the
// worker can switch to must be embedded.
func TestFilesHoldEverythingTheManifestLoads(t *testing.T) {
	data, err := Files.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Background struct {
			ServiceWorker string `json:"service_worker"`
		} `json:"background"`
		Action struct {
			Popup string `json:"default_popup"`
		} `json:"action"`
		Icons map[string]string `json:"icons"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	want := []string{manifest.Background.ServiceWorker, manifest.Action.Popup}
	for _, icon := range manifest.Icons {
		want = append(want, icon)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".test.js") {
			want = append(want, name)
		}
	}
	icons, err := os.ReadDir("icons")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range icons {
		if name := entry.Name(); strings.HasSuffix(name, ".png") {
			want = append(want, "icons/"+name)
		}
	}
	for _, name := range want {
		if _, err := fs.Stat(Files, name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
}
