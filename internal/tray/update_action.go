package tray

import (
	"context"
	"fmt"
	"sync"

	"github.com/tnunamak/clawmeter/internal/update"
)

type updateOperations struct {
	ExecutablePath func() (string, error)
	ApplyTo        func(context.Context, string, string) error
	Restart        func(string) error
}

type updateAttempt struct {
	Title   string
	Quit    bool
	Updated bool
	Err     error
}

// updateAction serializes clicks, not effects. pendingPath is recorded before
// calling Restart, so a failed restart cannot cause another download/apply.
type updateAction struct {
	mu            sync.Mutex
	running       bool
	pendingPath   string
	quitRequested bool
}

func (a *updateAction) restartPending() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingPath != ""
}

func (a *updateAction) run(ctx context.Context, rel *update.Release, ops updateOperations) (out updateAttempt, started bool) {
	a.mu.Lock()
	if a.running || a.quitRequested || (a.pendingPath == "" && rel == nil) {
		a.mu.Unlock()
		return updateAttempt{}, false
	}
	a.running = true
	executable := a.pendingPath
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()
	started = true
	fail := func(err error) (updateAttempt, bool) {
		out.Err = err
		out.Title = fmt.Sprintf("Update failed: %v", err)
		return out, true
	}
	if executable == "" {
		if ops.ExecutablePath == nil || ops.ApplyTo == nil || ops.Restart == nil {
			return fail(fmt.Errorf("update operations unavailable"))
		}
		var err error
		executable, err = ops.ExecutablePath()
		if err != nil {
			return fail(err)
		}
		if executable == "" {
			return fail(fmt.Errorf("executable path unavailable"))
		}
		if err = ops.ApplyTo(ctx, rel.URL, executable); err != nil {
			return fail(err)
		}
		out.Updated = true
		a.mu.Lock()
		a.pendingPath = executable
		a.mu.Unlock()
	}
	var err error
	if ops.Restart == nil {
		err = fmt.Errorf("restart operation unavailable")
	} else {
		err = ops.Restart(executable)
	}
	if err != nil {
		out.Title = "Updated — restart Clawmeter"
		out.Err = err
		return out, true
	}
	a.mu.Lock()
	a.pendingPath = ""
	a.quitRequested = true
	a.mu.Unlock()
	out.Title = "Updated — restarting"
	out.Quit = true
	return out, true
}
