//go:build !windows && !darwin

package systray

import (
	"bufio"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// This test owns a private bus; it never contacts the desktop session bus.
func TestTooltipUpdatesRegisteredPropertyAndNotifiesHost(t *testing.T) {
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is required for the private-bus integration test")
	}
	cmd := exec.Command(daemon, "--session", "--nofork", "--print-address=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	connect := func() *dbus.Conn {
		t.Helper()
		conn, err := dbus.Connect(strings.TrimSpace(address))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	server, host := connect(), connect()
	tr := &tray{conn: server, tooltipTitle: "Codex · 7-Day\n92% used"}
	tr.props, err = prop.Export(server, path, tr.createPropSpec())
	if err != nil {
		t.Fatal(err)
	}
	previous := instance
	instance = tr
	t.Cleanup(func() { instance = previous })
	if err := host.AddMatchSignal(dbus.WithMatchInterface("org.kde.StatusNotifierItem"), dbus.WithMatchMember("NewToolTip")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 4)
	host.Signal(signals)
	read := func() tooltip {
		t.Helper()
		v, err := host.Object(server.Names()[0], path).GetProperty("org.kde.StatusNotifierItem.ToolTip")
		if err != nil {
			t.Fatal(err)
		}
		var result tooltip
		if err := dbus.Store([]any{v.Value()}, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := read(); got.V2 != "Codex · 7-Day" || got.V3 != "<p><b>92% used</b></p>" {
		t.Fatalf("registered tooltip = %#v", got)
	}
	for _, tc := range []struct{ text, title, body string }{
		{"Pinned · Work\nLast known <92%> & stale\nResets tomorrow", "Pinned · Work", "<p><b>Last known &lt;92%&gt; &amp; stale</b><br/>Resets tomorrow</p>"},
		{"", "", ""},
	} {
		SetTooltip(tc.text)
		select {
		case signal := <-signals:
			if signal.Path != path || signal.Name != "org.kde.StatusNotifierItem.NewToolTip" {
				t.Fatalf("host notification = %+v", signal)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("host did not receive NewToolTip")
		}
		if got := read(); got.V2 != tc.title || got.V3 != tc.body {
			t.Fatalf("updated tooltip = %#v; want title %q body %q", got, tc.title, tc.body)
		}
	}
}
