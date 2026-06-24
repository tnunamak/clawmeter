#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
Usage: packaging/winget/generate.sh vX.Y.Z [setup|portable]

Default mode is "setup", which targets ClawmeterSetup.exe and is the public
WinGet path. "portable" is only for legacy/local rehearsal against old releases.
EOF
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage
  exit 2
fi

tag="$1"
version="${tag#v}"
mode="${2:-setup}"

repo="tnunamak/clawmeter"
case "$mode" in
  setup)
    asset="ClawmeterSetup.exe"
    installer_type="inno"
    ;;
  portable)
    asset="clawmeter-windows-amd64.exe"
    installer_type="portable"
    ;;
  *)
    usage
    exit 2
    ;;
esac

url="https://github.com/${repo}/releases/download/${tag}/${asset}"
out_dir="packaging/winget/out/manifests/t/tnunamak/Clawmeter/${version}"
tmp="$(mktemp)"

cleanup() {
  rm -f "$tmp"
}
trap cleanup EXIT

if [[ -n "${WINGET_ASSET_PATH:-}" ]]; then
  if [[ ! -f "$WINGET_ASSET_PATH" ]]; then
    echo "WINGET_ASSET_PATH does not exist: ${WINGET_ASSET_PATH}" >&2
    exit 1
  fi
  echo "Hashing ${WINGET_ASSET_PATH}" >&2
  sha256="$(sha256sum "$WINGET_ASSET_PATH" | awk '{ print toupper($1) }')"
else
  echo "Downloading ${url}" >&2
  curl -fsSL "$url" -o "$tmp"
  sha256="$(sha256sum "$tmp" | awk '{ print toupper($1) }')"
fi

rm -rf "$out_dir"
mkdir -p "$out_dir"

cat >"${out_dir}/tnunamak.Clawmeter.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.1.12.0.schema.json
PackageIdentifier: tnunamak.Clawmeter
PackageVersion: ${version}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.12.0
EOF

cat >"${out_dir}/tnunamak.Clawmeter.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.1.12.0.schema.json
PackageIdentifier: tnunamak.Clawmeter
PackageVersion: ${version}
PackageLocale: en-US
Publisher: Tim Nunamaker
PublisherUrl: https://github.com/tnunamak
PackageName: Clawmeter
PackageUrl: https://github.com/tnunamak/clawmeter
License: MIT
LicenseUrl: https://raw.githubusercontent.com/tnunamak/clawmeter/main/LICENSE
ShortDescription: System tray and CLI quota meter for AI coding tools.
Description: Clawmeter shows AI coding quota status in a system tray icon and command line interface.
Moniker: clawmeter
Tags:
- ai
- cli
- tray
- quota
- codex
- claude
ManifestType: defaultLocale
ManifestVersion: 1.12.0
EOF

cat >"${out_dir}/tnunamak.Clawmeter.installer.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.1.12.0.schema.json
PackageIdentifier: tnunamak.Clawmeter
PackageVersion: ${version}
InstallerType: ${installer_type}
Commands:
- clawmeter
Installers:
- Architecture: x64
  InstallerUrl: ${url}
  InstallerSha256: ${sha256}
  Scope: user
EOF

if [[ "$mode" == "setup" ]]; then
  cat >>"${out_dir}/tnunamak.Clawmeter.installer.yaml" <<'EOF'
InstallerSwitches:
  Silent: /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /TASKS=addtopath
  SilentWithProgress: /SILENT /SUPPRESSMSGBOXES /NORESTART /TASKS=addtopath
UpgradeBehavior: install
EOF
fi

cat >>"${out_dir}/tnunamak.Clawmeter.installer.yaml" <<'EOF'
ManifestType: installer
ManifestVersion: 1.12.0
EOF

cat >&2 <<EOF
Generated WinGet manifests:
  ${out_dir}

Validate on Windows:
  winget validate "${out_dir}"

Closest pre-submission install rehearsal:
  winget settings --enable LocalManifestFiles
  winget install --manifest "${out_dir}" --accept-source-agreements --accept-package-agreements
EOF
