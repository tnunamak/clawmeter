#!/bin/sh
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALLER="${INSTALLER_SH:-$SCRIPT_DIR/install.sh}"
SANDBOX="$(mktemp -d "${HOME}/.tmp/clawmeter-install-test.XXXXXX")"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin" "$SANDBOX/home" "$SANDBOX/install"
export HOME="$SANDBOX/home" INSTALL_DIR="$SANDBOX/install" NO_MODIFY_PATH=1
export MOCK_DIR="$SANDBOX" MOCK_API_STATUS=403
export MOCK_LOCATION='https://github.com/tnunamak/clawmeter/releases/tag/v0.39.0'
export GITHUB_TOKEN='test-token' GH_TOKEN='other-test-token'
case "$(uname -s):$(uname -m)" in
  Linux:x86_64) MOCK_ASSET_NAME='clawmeter-linux-amd64' ;;
  Linux:aarch64|Linux:arm64) MOCK_ASSET_NAME='clawmeter-linux-arm64' ;;
  Darwin:x86_64) MOCK_ASSET_NAME='clawmeter-darwin-amd64' ;;
  Darwin:arm64) MOCK_ASSET_NAME='clawmeter-darwin-arm64' ;;
  *) printf 'SKIP: unsupported test platform\n'; exit 0 ;;
esac
export MOCK_ASSET_NAME

cat > "$SANDBOX/binary" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$SANDBOX/binary"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$SANDBOX/binary" | awk -v name="$MOCK_ASSET_NAME" '{ print $1 "  " name }' > "$SANDBOX/SHA256SUMS.txt"
else
  shasum -a 256 "$SANDBOX/binary" | awk -v name="$MOCK_ASSET_NAME" '{ print $1 "  " name }' > "$SANDBOX/SHA256SUMS.txt"
fi

cat > "$SANDBOX/bin/curl" <<'EOF'
#!/bin/sh
out='' headers='' auth=0 url=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|-D) flag="$1"; shift; if [ "$flag" = -o ]; then out="$1"; else headers="$1"; fi ;;
    -H) shift; [ "$1" = "Authorization: Bearer ${GITHUB_TOKEN:-${GH_TOKEN:-}}" ] || exit 90; auth=1 ;;
    -w) shift ;;
    http*) url="$1" ;;
  esac
  shift
done
case "$url" in
  https://api.github.com/*)
    [ "$auth" = 1 ] || exit 91
    printf 'api\n' >> "$MOCK_DIR/requests"
    [ "$MOCK_API_STATUS" = network ] && exit 7
    [ "$MOCK_API_STATUS" = 200 ] || exit 22
    printf '[{"tag_name":"v0.39.0"}]\n' > "$out"
    printf '200' ;;
  https://github.com/tnunamak/clawmeter/releases/latest)
    [ "$auth" = 0 ] || exit 92
    printf 'latest\n' >> "$MOCK_DIR/requests"
    printf 'HTTP/2 302\r\nLocation: %s\r\n\r\n' "$MOCK_LOCATION" > "$headers" ;;
  https://github.com/tnunamak/clawmeter/releases/download/*/SHA256SUMS.txt)
    [ "$auth" = 0 ] || exit 93
    printf 'sums\n' >> "$MOCK_DIR/requests"
    cp "$MOCK_DIR/SHA256SUMS.txt" "$out" ;;
  https://github.com/tnunamak/clawmeter/releases/download/*)
    [ "$auth" = 0 ] || exit 94
    printf 'asset\n' >> "$MOCK_DIR/requests"
    if [ -n "$out" ]; then cp "$MOCK_DIR/binary" "$out"; fi ;;
  https://raw.githubusercontent.com/*)
    [ "$auth" = 0 ] || exit 95
    printf 'icon\n' >> "$MOCK_DIR/requests"
    printf 'icon' > "$out" ;;
  *) exit 96 ;;
esac
EOF
cat > "$SANDBOX/bin/pkill" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$SANDBOX/bin/curl" "$SANDBOX/bin/pkill"
export PATH="$SANDBOX/bin:$PATH"

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }
run_install() { sh "$INSTALLER" > "$SANDBOX/output" 2>&1; }

run_install || fail "403 fallback install failed: $(cat "$SANDBOX/output")"
grep -q 'falling back' "$SANDBOX/output" || fail '403 fallback was not announced'
[ -x "$INSTALL_DIR/clawmeter" ] || fail '403 fallback did not install the binary'
grep -q '^latest$' "$SANDBOX/requests" || fail 'latest redirect was not requested'
if grep -q 'test-token' "$SANDBOX/output"; then fail 'token appeared in installer output'; fi
printf 'PASS: shell 403 fallback and API-only token\n'

cp "$SANDBOX/SHA256SUMS.txt" "$SANDBOX/SHA256SUMS.good"
printf '%064d  %s\n' 0 "$MOCK_ASSET_NAME" > "$SANDBOX/SHA256SUMS.txt"
run_install && fail 'checksum mismatch was accepted after fallback'
cmp -s "$SANDBOX/binary" "$INSTALL_DIR/clawmeter" || fail 'checksum mismatch changed the installed binary'
mv "$SANDBOX/SHA256SUMS.good" "$SANDBOX/SHA256SUMS.txt"
printf 'PASS: shell fallback preserves checksum gate\n'

rm -f "$INSTALL_DIR/clawmeter" "$SANDBOX/requests"
MOCK_LOCATION='https://github.com/tnunamak/clawmeter/releases/tag/v01.2.3' \
  run_install && fail 'invalid fallback tag was accepted'
[ ! -e "$INSTALL_DIR/clawmeter" ] || fail 'invalid tag installed a binary'
printf 'PASS: shell rejects invalid fallback tag\n'

MOCK_LOCATION='https://github.com/tnunamak/clawmeter/releases/tag/extra/v0.39.0' \
  run_install && fail 'fallback accepted a redirect with an extra path segment'
[ ! -e "$INSTALL_DIR/clawmeter" ] || fail 'extra path segment installed a binary'
printf 'PASS: shell rejects noncanonical redirect path\n'

rm -f "$SANDBOX/requests"
unset GITHUB_TOKEN
export MOCK_API_STATUS=429
run_install || fail "GH_TOKEN 429 fallback failed: $(cat "$SANDBOX/output")"
grep -q 'falling back' "$SANDBOX/output" || fail '429 fallback was not announced'
printf 'PASS: shell GH_TOKEN fallback on 429\n'

rm -f "$SANDBOX/requests"
export MOCK_API_STATUS=network
run_install || fail "network fallback failed: $(cat "$SANDBOX/output")"
grep -q 'falling back' "$SANDBOX/output" || fail 'network fallback was not announced'
printf 'PASS: shell fallback on network failure\n'

rm -f "$SANDBOX/requests"
export MOCK_API_STATUS=200
run_install || fail "API install failed: $(cat "$SANDBOX/output")"
if grep -q '^latest$' "$SANDBOX/requests"; then fail 'API success used fallback'; fi
printf 'PASS: shell API success\n'

if command -v pwsh >/dev/null 2>&1; then
  export LOCALAPPDATA="$SANDBOX/home"
  export INSTALLER_PS="${INSTALLER_PS:-$SCRIPT_DIR/install.ps1}"
  export GITHUB_TOKEN='test-token'
  cat > "$SANDBOX/pwsh-test.ps1" <<'EOF'
$ErrorActionPreference = 'Stop'
$Repo = 'tnunamak/clawmeter'
$AssetName = 'clawmeter-windows-amd64.exe'
function Say([string]$Message) { Write-Host $Message }
$tokens = $null
$errors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile($env:INSTALLER_PS, [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw 'install.ps1 has parse errors' }
$functionAst = $ast.Find({ param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Get-LatestReleaseAsset' }, $true)
Invoke-Expression $functionAst.Extent.Text
$script:requests = @()
$script:location = 'https://github.com/tnunamak/clawmeter/releases/tag/v0.39.0'
function Invoke-RestMethod {
    param($Uri, $Headers, $MaximumRedirection)
    if ($Uri -notlike 'https://api.github.com/*') { throw 'unexpected API URL' }
    $expectedToken = if ($env:GITHUB_TOKEN) { $env:GITHUB_TOKEN } else { $env:GH_TOKEN }
    if ($Headers.Authorization -ne "Bearer $expectedToken") { throw 'missing API token' }
    $script:requests += 'api'
    throw '403'
}
function Invoke-WebRequest {
    param($Uri, $Method, $MaximumRedirection, $Headers)
    if ($Uri -ne 'https://github.com/tnunamak/clawmeter/releases/latest') { throw 'unexpected redirect URL' }
    if ($MaximumRedirection -ne 0) { throw 'redirects were followed' }
    if ($Headers.Authorization) { throw 'token sent to github.com' }
    $script:requests += 'latest'
    [pscustomobject]@{ Headers = @{ Location = $script:location } }
}
$release = Get-LatestReleaseAsset
if ($release.Version -ne 'v0.39.0' -or $script:requests.Count -ne 2) { throw 'PowerShell fallback failed' }
Write-Host 'PASS: PowerShell 403 fallback and API-only token'
$script:location = 'https://github.com/tnunamak/clawmeter/releases/tag/v01.2.3'
try {
    Get-LatestReleaseAsset | Out-Null
    throw 'PowerShell accepted an invalid fallback tag'
} catch {
    if ($_.Exception.Message -notlike '*valid version tag*') { throw }
}
Write-Host 'PASS: PowerShell rejects invalid fallback tag'
$script:location = 'https://github.com/tnunamak/clawmeter/releases/tag/extra/v0.39.0'
try {
    Get-LatestReleaseAsset | Out-Null
    throw 'PowerShell accepted an extra redirect path segment'
} catch {
    if ($_.Exception.Message -notlike '*valid version tag*') { throw }
}
Write-Host 'PASS: PowerShell rejects noncanonical redirect path'
$script:location = 'https://github.com/tnunamak/clawmeter/releases/tag/v0.39.0'
Remove-Item Env:GITHUB_TOKEN
$release = Get-LatestReleaseAsset
if ($release.Version -ne 'v0.39.0') { throw 'PowerShell GH_TOKEN fallback failed' }
Write-Host 'PASS: PowerShell GH_TOKEN fallback'
EOF
  pwsh -NoProfile -File "$SANDBOX/pwsh-test.ps1"
else
  printf 'SKIP: PowerShell fallback test (pwsh unavailable)\n'
fi
