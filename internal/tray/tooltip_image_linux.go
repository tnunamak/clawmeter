//go:build tray && linux

package tray

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/tnunamak/clawmeter/internal/meter"
	"github.com/tnunamak/clawmeter/internal/systray"
	"github.com/tnunamak/clawmeter/internal/tray/icons"
	"github.com/tnunamak/clawmeter/internal/view"
)

// Zero withholds images when the native host or window theme is unknown.
var tooltipColorScheme atomic.Uint32
var tooltipWatcher struct {
	sync.Mutex
	cancel context.CancelFunc
}

func supportsTooltipImages(desktop string) bool {
	known := false
	for _, token := range strings.Split(strings.ToLower(desktop), ":") {
		switch strings.TrimSpace(token) {
		case "kde", "plasma":
			known = true
		case "gnome", "xfce", "cinnamon", "unity":
			return false
		}
	}
	return known
}

func tooltipScheme(value any) uint32 {
	for i := 0; i < 4; i++ {
		if v, ok := value.(dbus.Variant); ok {
			value = v.Value()
			continue
		}
		switch v := value.(type) {
		case uint32:
			if v == 1 || v == 2 {
				return v
			}
		case int32:
			if v == 1 || v == 2 {
				return uint32(v)
			}
		}
		return 0
	}
	return 0
}

func changeTooltipScheme(value any) {
	scheme := tooltipScheme(value)
	if tooltipColorScheme.Swap(scheme) != scheme {
		select {
		case tooltipAppearanceChanged <- struct{}{}:
		default:
		}
	}
}

// This connection follows window appearance independently of a forced panel
// icon palette. Rendering never performs portal I/O.
func setupTooltipColors() {
	if !supportsTooltipImages(os.Getenv("XDG_CURRENT_DESKTOP")) {
		return
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	if err = conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.portal.Settings"), dbus.WithMatchMember("SettingChanged"), dbus.WithMatchSender("org.freedesktop.portal.Desktop"), dbus.WithMatchObjectPath("/org/freedesktop/portal/desktop")); err != nil {
		conn.RemoveSignal(signals)
		conn.Close()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	tooltipWatcher.Lock()
	tooltipWatcher.cancel = cancel
	tooltipWatcher.Unlock()
	var value dbus.Variant
	readCtx, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	err = conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop").CallWithContext(readCtx, "org.freedesktop.portal.Settings.Read", 0, "org.freedesktop.appearance", "color-scheme").Store(&value)
	stop()
	if err == nil {
		changeTooltipScheme(value)
	}
	go func() {
		defer conn.Close()
		defer conn.RemoveSignal(signals)
		for {
			select {
			case <-ctx.Done():
				return
			case <-conn.Context().Done():
				changeTooltipScheme(uint32(0))
				return
			case signal, open := <-signals:
				if !open {
					return
				}
				if signal == nil || len(signal.Body) != 3 || signal.Body[0] != "org.freedesktop.appearance" || signal.Body[1] != "color-scheme" {
					continue
				}
				changeTooltipScheme(signal.Body[2])
			}
		}
	}()
}

func stopTooltipColors() {
	tooltipWatcher.Lock()
	defer tooltipWatcher.Unlock()
	if tooltipWatcher.cancel != nil {
		tooltipWatcher.cancel()
		tooltipWatcher.cancel = nil
	}
	tooltipColorScheme.Store(0)
}

func nativeTooltipImage(snap view.Snapshot) tooltipImage {
	scheme := tooltipColorScheme.Load()
	if scheme == 0 {
		return tooltipImage{}
	}
	acct, window, ok := selectedWindow(snap)
	if !ok {
		return tooltipImage{}
	}
	cells := tooltipCells(acct, window, snap.At)
	palette := icons.TrayPaletteDark
	if scheme == 2 {
		palette = icons.TrayPaletteLight
	}
	image := icons.HoverMeterSVG(cells, palette)
	if len(image) == 0 {
		return tooltipImage{}
	}
	return tooltipImage{Alt: meter.BarText(cells), SVG: string(image)}
}

func setNativeTooltip(text string, image tooltipImage) {
	systray.SetTooltipWithSVG(text, image.Alt, []byte(image.SVG))
}
