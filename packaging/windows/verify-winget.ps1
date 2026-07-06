[CmdletBinding()]
param(
    [string]$PackageId = "tnunamak.Clawmeter",

    [string]$ExpectedVersion,

    [switch]$SkipUninstall
)

$ErrorActionPreference = "Stop"

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

function Invoke-WinGetChecked {
    param(
        [string]$Message,
        [string[]]$Arguments
    )

    Write-Host "winget $($Arguments -join ' ')"
    $output = & winget @Arguments 2>&1 | Out-String
    $exitCode = $LASTEXITCODE
    if (-not [string]::IsNullOrWhiteSpace($output)) {
        $output | Write-Host
    }
    Assert-True -Condition ($exitCode -eq 0) -Message "$Message exited 0"
    return $output
}

function Resolve-ClawmeterExe {
    $installDir = Join-Path $env:LOCALAPPDATA "Programs\Clawmeter"
    $exePath = Join-Path $installDir "clawmeter.exe"
    if (Test-Path $exePath) {
        return $exePath
    }

    $command = Get-Command clawmeter -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }

    throw "FAIL: could not find installed clawmeter.exe"
}

function Assert-CommandOutput {
    param(
        [string]$Message,
        [scriptblock]$Command
    )

    $output = & $Command 2>&1 | Out-String
    Assert-True -Condition ($LASTEXITCODE -eq 0) -Message "$Message exited 0"
    Assert-True -Condition (-not [string]::IsNullOrWhiteSpace($output)) -Message "$Message produced output"
    $output | Write-Host
    return $output
}

function Wait-PathGone {
    param(
        [string]$Path,
        [int]$TimeoutSeconds = 30
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Test-Path $Path) -and ((Get-Date) -lt $deadline)) {
        Start-Sleep -Milliseconds 500
    }
}

Assert-True -Condition ($null -ne (Get-Command winget -ErrorAction SilentlyContinue)) -Message "winget is available"

Invoke-WinGetChecked -Message "winget source update" -Arguments @("source", "update")

$showOutput = Invoke-WinGetChecked -Message "winget show" -Arguments @(
    "show",
    "--id", $PackageId,
    "-e",
    "--source", "winget",
    "--accept-source-agreements",
    "--disable-interactivity"
)
Assert-True -Condition ($showOutput -match [regex]::Escape($PackageId)) -Message "winget default source exposes $PackageId"
if ($ExpectedVersion) {
    Assert-True -Condition ($showOutput -match "Version:\s+$([regex]::Escape($ExpectedVersion))") -Message "winget shows expected version $ExpectedVersion"
}

Invoke-WinGetChecked -Message "winget install" -Arguments @(
    "install",
    "--id", $PackageId,
    "-e",
    "--source", "winget",
    "--silent",
    "--accept-source-agreements",
    "--accept-package-agreements",
    "--disable-interactivity"
)

$exePath = Resolve-ClawmeterExe
$installDir = Split-Path $exePath -Parent
$startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\Clawmeter\Clawmeter.lnk"
$uninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{92EEFACA-DA48-4099-937D-F16E591F1DEE}_is1"

Assert-True -Condition (Test-Path $exePath) -Message "installed clawmeter.exe"
Assert-True -Condition (Test-Path $startMenu) -Message "created Start Menu shortcut"
Assert-True -Condition (Test-Path $uninstallKey) -Message "created uninstall registry entry"

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($null -eq $userPath) {
    $userPath = ""
}
$pathParts = @($userPath -split ";" | ForEach-Object { $_.TrimEnd("\") })
Assert-True -Condition ($pathParts -contains $installDir.TrimEnd("\")) -Message "added install directory to user PATH"

$versionOutput = Assert-CommandOutput -Message "clawmeter version" -Command { & $exePath version }
if ($ExpectedVersion) {
    Assert-True -Condition ($versionOutput -match [regex]::Escape($ExpectedVersion)) -Message "installed binary reports expected version $ExpectedVersion"
}

Assert-CommandOutput -Message "clawmeter providers" -Command { & $exePath providers }
Assert-CommandOutput -Message "bare clawmeter" -Command { & $exePath }

Invoke-WinGetChecked -Message "winget list" -Arguments @(
    "list",
    "--id", $PackageId,
    "-e",
    "--source", "winget",
    "--accept-source-agreements",
    "--disable-interactivity"
)

if (-not $SkipUninstall) {
    Invoke-WinGetChecked -Message "winget uninstall" -Arguments @(
        "uninstall",
        "--id", $PackageId,
        "-e",
        "--silent",
        "--disable-interactivity"
    )
    Wait-PathGone -Path $exePath
    Assert-True -Condition (-not (Test-Path $exePath)) -Message "winget uninstall removed clawmeter.exe"
}

Write-Host "WinGet verification finished."
