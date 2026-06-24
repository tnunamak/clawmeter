# Third-party components

Clawmeter is released under the MIT license. Windows release artifacts are built from the Go module graph and the local `systray-fork` replacement in this repository.

## Windows tray build license audit

This table is based on:

```bash
GOOS=windows GOARCH=amd64 GOFLAGS='-tags=tray' go-licenses report ./cmd/clawmeter
```

| Component | Use | License |
| --- | --- | --- |
| `github.com/tnunamak/clawmeter` | application code | MIT |
| `fyne.io/systray` via `./systray-fork` | system tray integration | Apache-2.0 |
| `github.com/tadvi/systray` | systray fork ancestry | MIT |
| `git.sr.ht/~jackmordaunt/go-toast` | Windows toast integration | MIT |
| `github.com/gen2brain/beeep` | desktop notification helper | BSD-2-Clause |
| `github.com/go-ole/go-ole` | Windows OLE support | MIT |
| `github.com/godbus/dbus/v5` | Linux desktop integration dependency | BSD-2-Clause |
| `github.com/jackmordaunt/icns/v3` | macOS icon support dependency | MIT |
| `github.com/nfnt/resize` | image resizing | ISC |
| `github.com/pkg/browser` | opening release/update links in the system browser | BSD-2-Clause |
| `github.com/sergeymakinen/go-bmp` | BMP image support | BSD-3-Clause |
| `github.com/sergeymakinen/go-ico` | ICO image support | BSD-3-Clause |
| `golang.org/x/image` | image encoding/decoding | BSD-3-Clause |
| `golang.org/x/sys/windows` | Windows system calls | BSD-3-Clause |
| `golang.org/x/term` | terminal support | BSD-3-Clause |
| `gopkg.in/yaml.v3` | YAML config parsing | MIT/Apache-2.0 |

## Release policy

Before applying for or using SignPath Foundation signing, re-run the Windows tray license audit and review any dependency changes. Clawmeter should sign only Clawmeter-built release artifacts; it should not intentionally bundle or sign upstream third-party binaries as separate products.
