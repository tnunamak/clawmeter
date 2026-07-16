# Third-party components

Clawmeter is released under the MIT license. Windows release artifacts are built from the Go module graph and the local `systray-fork` replacement in this repository.

## Bundled assets

| Asset | Use | Source and canonical identity check | License / redistribution rationale |
| --- | --- | --- | --- |
| `internal/tray/icons/provider-grok.png` | Grok provider tray mark, recolored to Clawmeter's tray palette | Adapted from CodexBar's `ProviderIcon-grok.svg` ([source](https://github.com/steipete/CodexBar)) | MIT, Copyright (c) 2026 Peter Steinberger |
| `internal/tray/icons/provider-jetbrains.png` | JetBrains provider tray mark | Rasterized from pinned CodexBar `ProviderIcon-jetbrains.svg` ([source](https://github.com/steipete/CodexBar/blob/6d71af30b84d8ee0b02361648b2123e0921a8277/Sources/CodexBar/Resources/ProviderIcon-jetbrains.svg)); identity checked against [JetBrains brand materials](https://www.jetbrains.com/company/brand/) | The pinned CodexBar checkout distributes this resource under its repository [MIT license](https://github.com/steipete/CodexBar/blob/6d71af30b84d8ee0b02361648b2123e0921a8277/LICENSE), which permits copying the copyrighted resource with attribution. The JetBrains mark remains a trademark; no trademark license is claimed. |
| `internal/tray/icons/provider-synthetic.png` | Synthetic provider tray mark | Rasterized from pinned CodexBar `ProviderIcon-synthetic.svg` ([source](https://github.com/steipete/CodexBar/blob/6d71af30b84d8ee0b02361648b2123e0921a8277/Sources/CodexBar/Resources/ProviderIcon-synthetic.svg)); identity checked against [Synthetic](https://synthetic.new/) | The pinned CodexBar checkout distributes this resource under its repository [MIT license](https://github.com/steipete/CodexBar/blob/6d71af30b84d8ee0b02361648b2123e0921a8277/LICENSE), which permits copying the copyrighted resource with attribution. The Synthetic mark remains a trademark; no trademark license is claimed. |
| `internal/tray/icons/provider-zai.png` | z.ai provider tray mark | Rasterized from pinned CodexBar `ProviderIcon-zai.svg` ([source](https://github.com/steipete/CodexBar/blob/6d71af30b84d8ee0b02361648b2123e0921a8277/Sources/CodexBar/Resources/ProviderIcon-zai.svg)); identity checked against [z.ai](https://z.ai/) | The pinned CodexBar checkout distributes this resource under its repository [MIT license](https://github.com/steipete/CodexBar/blob/6d71af30b84d8ee0b02361648b2123e0921a8277/LICENSE), which permits copying the copyrighted resource with attribution. The z.ai mark remains a trademark; no trademark license is claimed. |

These PNGs are fixed 128px RGBA rasterizations of the cited vectors and are downscaled at
runtime for tray sizes. They are not modified brand marks and are used only to identify the
provider in the tray. The pinned local source paths used for this audit are listed in the
implementation report.

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
