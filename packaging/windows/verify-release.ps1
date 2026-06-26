[CmdletBinding()]
param(
    [string]$Version,

    [string]$OutputDir = (Join-Path $env:TEMP "clawmeter-release-verify"),

    [switch]$IncludeStartup,

    [switch]$DisableUpdates,

    [switch]$ScanWithDefender,

    [switch]$SkipInstall
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

function Resolve-ClawmeterTag {
    param([string]$RequestedVersion)

    if ($RequestedVersion) {
        if ($RequestedVersion.StartsWith("v")) {
            return $RequestedVersion
        }
        return "v$RequestedVersion"
    }

    $release = Invoke-RestMethod `
        -Uri "https://api.github.com/repos/tnunamak/clawmeter/releases/latest" `
        -Headers @{ "User-Agent" = "clawmeter-release-verifier" }
    return $release.tag_name
}

function Assert-True {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw "FAIL: $Message"
    }

    Write-Host "PASS: $Message"
}

$tag = Resolve-ClawmeterTag -RequestedVersion $Version
$out = New-Item -ItemType Directory -Force -Path $OutputDir
$base = "https://github.com/tnunamak/clawmeter/releases/download/$tag"
$installer = Join-Path $out.FullName "ClawmeterSetup.exe"
$checksums = Join-Path $out.FullName "SHA256SUMS.txt"

Write-Host "Verifying Clawmeter $tag from $base"

Invoke-WebRequest "$base/ClawmeterSetup.exe" -OutFile $installer
Invoke-WebRequest "$base/SHA256SUMS.txt" -OutFile $checksums

$line = Get-Content $checksums | Where-Object { $_ -match "\sClawmeterSetup\.exe$" } | Select-Object -First 1
Assert-True -Condition (-not [string]::IsNullOrWhiteSpace($line)) -Message "SHA256SUMS.txt contains ClawmeterSetup.exe"

$expectedHash = (($line -split "\s+")[0]).ToUpperInvariant()
$actualHash = (Get-FileHash $installer -Algorithm SHA256).Hash.ToUpperInvariant()
Assert-True -Condition ($actualHash -eq $expectedHash) -Message "installer hash matches SHA256SUMS.txt"

if ($ScanWithDefender) {
    & (Join-Path $PSScriptRoot "collect-defender-evidence.ps1") -Path $installer -OutputDir (Join-Path $out.FullName "defender") -Scan
}

if (-not $SkipInstall) {
    $verifyArgs = @{
        InstallerPath = $installer
    }
    if ($IncludeStartup) {
        $verifyArgs.IncludeStartup = $true
    }
    if ($DisableUpdates) {
        $verifyArgs.DisableUpdates = $true
    }

    & (Join-Path $PSScriptRoot "verify-installer.ps1") @verifyArgs
}

Write-Host "Release verification finished: $($out.FullName)"
