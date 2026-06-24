module github.com/tnunamak/clawmeter

go 1.25.0

require (
	fyne.io/systray v1.12.0
	github.com/gen2brain/beeep v0.11.2
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c
	golang.org/x/image v0.36.0
	golang.org/x/sys v0.46.0
	golang.org/x/term v0.44.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	git.sr.ht/~jackmordaunt/go-toast v1.1.2 // indirect
	github.com/esiqveland/notify v0.13.3 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/jackmordaunt/icns/v3 v3.0.1 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/sergeymakinen/go-bmp v1.0.0 // indirect
	github.com/sergeymakinen/go-ico v1.0.0-beta.0 // indirect
	github.com/tadvi/systray v0.0.0-20190226123456-11a2b8fa57af // indirect
)

replace fyne.io/systray v1.12.0 => ./systray-fork
