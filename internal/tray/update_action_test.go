package tray

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tnunamak/clawmeter/internal/update"
)

func TestUpdateActionNilRelease(t *testing.T) {
	var a updateAction
	got, started := a.run(context.Background(), nil, updateOperations{})
	if started || got != (updateAttempt{}) || a.restartPending() {
		t.Fatalf("%+v %v", got, started)
	}
}

func TestUpdateActionRestartRetryUsesSavedPath(t *testing.T) {
	for _, changed := range []*update.Release{nil, {URL: "different"}} {
		var a updateAction
		paths, applies, restarts := 0, 0, 0
		boom := errors.New("restart failed")
		ops := updateOperations{
			ExecutablePath: func() (string, error) { paths++; return "/fixture/app", nil },
			ApplyTo: func(_ context.Context, url, path string) error {
				applies++
				if url != "original" || path != "/fixture/app" {
					t.Fatal(url, path)
				}
				return nil
			},
			Restart: func(path string) error {
				restarts++
				if path != "/fixture/app" {
					t.Fatal(path)
				}
				if !a.restartPending() {
					t.Fatal("pending path not published before restart")
				}
				if restarts == 1 {
					return boom
				}
				return nil
			},
		}
		first, started := a.run(context.Background(), &update.Release{URL: "original"}, ops)
		if !started || !first.Updated || first.Quit || first.Err != boom || first.Title != "Updated — restart Clawmeter" || !a.restartPending() {
			t.Fatalf("%+v", first)
		}
		second, started := a.run(context.Background(), changed, ops)
		if !started || second.Updated || !second.Quit || second.Err != nil || a.restartPending() {
			t.Fatalf("%+v", second)
		}
		if paths != 1 || applies != 1 || restarts != 2 {
			t.Fatal(paths, applies, restarts)
		}
		if _, started := a.run(context.Background(), &update.Release{URL: "queued-click"}, ops); started {
			t.Fatal("a queued click started another update after quit was requested")
		}
	}
}

func TestUpdateActionFailuresAllowInstallRetry(t *testing.T) {
	for _, phase := range []string{"path", "apply"} {
		var a updateAction
		boom := errors.New(phase + " failed")
		calls, restart := 0, 0
		ops := updateOperations{
			ExecutablePath: func() (string, error) {
				if phase == "path" && calls == 0 {
					calls++
					return "", boom
				}
				return "/fixture/app", nil
			},
			ApplyTo: func(context.Context, string, string) error {
				if phase == "apply" && calls == 0 {
					calls++
					return boom
				}
				return nil
			},
			Restart: func(string) error { restart++; return nil },
		}
		got, started := a.run(context.Background(), &update.Release{}, ops)
		if !started || got.Err != boom || got.Updated || got.Quit || a.restartPending() || restart != 0 {
			t.Fatalf("%s %+v", phase, got)
		}
		got, started = a.run(context.Background(), &update.Release{}, ops)
		if !started || got.Err != nil || !got.Updated || !got.Quit || restart != 1 {
			t.Fatalf("%s %+v", phase, got)
		}
	}
}

func TestUpdateActionDuplicateClicksAndNonblockingState(t *testing.T) {
	for _, phase := range []string{"apply", "restart"} {
		var a updateAction
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan updateAttempt, 1)
		var applies atomic.Int32
		hold := func() { close(entered); <-release }
		ops := updateOperations{
			ExecutablePath: func() (string, error) { return "/fixture/app", nil },
			ApplyTo: func(context.Context, string, string) error {
				applies.Add(1)
				if phase == "apply" {
					hold()
				}
				return nil
			},
			Restart: func(string) error {
				if phase == "restart" {
					hold()
				}
				return nil
			},
		}
		go func() { out, _ := a.run(context.Background(), &update.Release{}, ops); done <- out }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("effect did not start")
		}
		state := make(chan bool, 1)
		go func() { state <- a.restartPending() }()
		select {
		case pending := <-state:
			if pending != (phase == "restart") {
				t.Fatal("wrong pending state")
			}
		case <-time.After(time.Second):
			close(release)
			t.Fatal("mutex held across effect")
		}
		duplicate := make(chan bool, 1)
		go func() { _, started := a.run(context.Background(), &update.Release{}, ops); duplicate <- started }()
		select {
		case started := <-duplicate:
			if started {
				t.Fatal("duplicate started")
			}
		case <-time.After(time.Second):
			close(release)
			t.Fatal("duplicate blocked")
		}
		close(release)
		select {
		case out := <-done:
			if !out.Quit || !out.Updated || out.Err != nil {
				t.Fatalf("%+v", out)
			}
		case <-time.After(time.Second):
			t.Fatal("completion blocked")
		}
		if applies.Load() != 1 {
			t.Fatal(applies.Load())
		}
	}
}

func TestUpdateActionMissingOperationsAndEmptyPath(t *testing.T) {
	var a updateAction
	if out, started := a.run(context.Background(), &update.Release{}, updateOperations{}); !started || out.Err == nil || out.Updated {
		t.Fatalf("%+v", out)
	}
	ops := updateOperations{ExecutablePath: func() (string, error) { return "", nil }, ApplyTo: func(context.Context, string, string) error { t.Fatal("apply"); return nil }, Restart: func(string) error { t.Fatal("restart"); return nil }}
	if out, _ := a.run(context.Background(), &update.Release{}, ops); out.Err == nil || a.restartPending() {
		t.Fatalf("%+v", out)
	}
}
