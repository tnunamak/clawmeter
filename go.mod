module github.com/tnunamak/clawmeter

go 1.24.0

require (
	fyne.io/systray v1.12.0
	golang.org/x/term v0.40.0
)

require (
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace fyne.io/systray v1.12.0 => ./systray-fork
