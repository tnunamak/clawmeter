//go:build tray

package tray

import "github.com/tnunamak/clawmeter/internal/update"

// updateMenuState coordinates background checks with the action currently
// shown on the native menu. All fields and publication callbacks use the
// native render lock; network and update effects run outside that lock.
type updateMenuState struct {
	generation uint64
	busy       bool
	recovery   bool
}

func (m *updateMenuState) check(fetch func() (*update.Release, error), publish func(*update.Release)) {
	trayRenderMu.Lock()
	if m.busy || m.recovery {
		trayRenderMu.Unlock()
		return
	}
	generation := m.generation
	trayRenderMu.Unlock()
	rel, err := fetch()
	if err != nil {
		return
	}
	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	if generation != m.generation || m.busy || m.recovery {
		return
	}
	publish(rel)
}

func (m *updateMenuState) begin(publish func()) bool {
	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	if m.busy {
		return false
	}
	m.busy = true
	m.generation++
	publish()
	return true
}

func (m *updateMenuState) finish(preserveAction bool, publish func()) {
	trayRenderMu.Lock()
	defer trayRenderMu.Unlock()
	m.busy = false
	m.recovery = preserveAction
	m.generation++
	publish()
}
