# SignPath Foundation readiness

This checklist maps SignPath Foundation's open-source conditions to Clawmeter repository evidence. It is not a guarantee of acceptance; SignPath still makes the final decision, including reputation judgment.

| Criterion | Repository evidence | Status |
| --- | --- | --- |
| No malware / no PUA | Clawmeter is a local quota/status CLI and tray app; release workflow builds from public source. | Satisfied by design; keep release artifacts source-built. |
| OSI-approved OSS license | [LICENSE](../LICENSE) is MIT. | Satisfied. |
| No proprietary components | [Third-party components](third-party-components.md) lists Windows tray build dependencies and concrete licenses. | Satisfied; re-run audit before signing. |
| Actively maintained | GitHub Releases, CI, Dependabot, and project history. | Satisfied as repository evidence; reputation remains subjective. |
| Already released in signed form factor | GitHub Releases publish `ClawmeterSetup.exe` and `clawmeter-windows-amd64.exe`. | Satisfied. |
| Functionality documented | [README](../README.md) documents CLI, tray, providers, installer, and release verification. | Satisfied. |
| Sign own projects/binaries only | SignPath artifact configuration targets only Clawmeter-built Windows exe artifacts. | Satisfied. |
| No hacking/exploit tool | Clawmeter monitors quota/rate-limit status; it is not a vulnerability scanner or exploit/circumvention tool. | Satisfied by product scope. |
| Privacy policy | [Privacy Policy](../PRIVACY.md) documents credentials, provider APIs, GitHub update checks, cache paths, and opt-out. | Satisfied. |
| Display privacy policy during installation | Windows Inno installer uses `InfoBeforeFile` with `PRIVACY.md`. | Satisfied. |
| Disable user-data transfer not specified by user | Provider polling can be disabled per provider; the Windows installer exposes an automatic update-check task, and update checks can also be disabled with `clawmeter config set check_for_updates false`. | Satisfied. |
| Warn before system changes | Installer task labels disclose PATH and startup changes. | Satisfied. |
| Provide uninstall | Installer creates uninstall entry and [Windows plan](../packaging/windows/PLAN.md) documents uninstall verification. | Satisfied. |
| MFA | [Code signing policy](code-signing.md) requires maintainer MFA for GitHub and SignPath. | Requires maintainer account setting outside repo. |
| Roles and approvers | [Code signing policy](code-signing.md) names maintainer/approver. | Satisfied for solo-maintainer project. |
| Code signing policy | README links [Code signing policy](code-signing.md), including SignPath required wording. | Satisfied. |
| Artifact metadata | Version resource script sets Windows PE metadata; SignPath artifact configuration enforces metadata. | Satisfied once SignPath project fields are configured. |
| Verifiable build origin | Release workflow uses GitHub-hosted runners and `actions/upload-artifact` before SignPath submission. | Satisfied by workflow shape. |
| Manual approval per signed release | [Code signing policy](code-signing.md) states each SignPath request requires manual approval. | Satisfied as policy; enforced in SignPath account. |
| Reputation | Public repo history, maintainer identity/history, release cadence, download/use evidence, external references. | Main remaining subjective risk. |

## Pre-application checks

Run these before applying or before enabling SignPath signing:

```bash
go test ./...
go test -tags tray ./...
GOOS=windows GOARCH=amd64 GOFLAGS='-tags=tray' go-licenses report ./cmd/clawmeter
yq '.' .signpath/policies/clawmeter/release-signing.yml >/dev/null
python3 - <<'PY'
import xml.etree.ElementTree as ET
ET.parse('.signpath/artifact-configurations/windows-release.xml')
PY
```
