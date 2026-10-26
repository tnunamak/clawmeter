//go:build tray

package tray

import (
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/update"
)

func TestUpdateMenuLateCheckCannotReplaceRecoveryAction(t *testing.T) {
	for _, recovery := range []string{"Updated — restart Clawmeter", "Homebrew installation · Show update steps…"} {
		for _, tc := range []struct {
			name     string
			response *update.Release
		}{{"no release", nil}, {"release", &update.Release{Version: "v9.9.9"}}} {
			t.Run(recovery+"/"+tc.name, func(t *testing.T) {
				var menu updateMenuState
				entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				title, visible, published := "Update to v9.9.9", true, false
				go func() {
					defer close(done)
					menu.check(func() (*update.Release, error) { close(entered); <-release; return tc.response, nil }, func(rel *update.Release) {
						published = true
						if rel == nil {
							visible = false
						} else {
							title = "Update to " + rel.Version
						}
					})
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("check did not start")
				}
				if !menu.begin(func() { title = "Updating…" }) {
					t.Fatal("update did not start")
				}
				menu.finish(true, func() { title = recovery })
				close(release)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("check did not finish")
				}
				if published || !visible || title != recovery {
					t.Fatalf("late check replaced recovery: published=%v visible=%v title=%q", published, visible, title)
				}
			})
		}
	}
}

func TestUpdateMenuChecksResumeAfterAnApplyFailure(t *testing.T) {
	var menu updateMenuState
	if !menu.begin(func() {}) {
		t.Fatal("first action rejected")
	}
	if menu.begin(func() { t.Fatal("duplicate action started") }) {
		t.Fatal("duplicate action accepted")
	}
	menu.check(func() (*update.Release, error) { t.Fatal("check fetched during update"); return nil, nil }, func(*update.Release) { t.Fatal("check published during update") })
	menu.finish(false, func() {})
	published := false
	menu.check(func() (*update.Release, error) { return &update.Release{Version: "v2.0.0"}, nil }, func(rel *update.Release) { published = rel.Version == "v2.0.0" })
	if !published {
		t.Fatal("checks remained blocked after retryable failure")
	}
}

func TestUpdateMenuOlderCheckDoesNotReplaceANewRetry(t *testing.T) {
	var menu updateMenuState
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	published := false
	go func() {
		defer close(done)
		menu.check(func() (*update.Release, error) { close(entered); <-release; return nil, nil }, func(*update.Release) { published = true })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	menu.begin(func() {})
	menu.finish(false, func() {})
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("check did not finish")
	}
	if published {
		t.Fatal("older check replaced a newer retry even though recovery is no longer blocked")
	}
}
