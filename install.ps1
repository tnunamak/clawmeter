[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\Clawmeter"),
    [switch]$Start,
    [switch]$Startup,
    [switch]$Uninstall,
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = "tnunamak/clawmeter"
$AssetName = "clawmeter-windows-amd64.exe"
$ExePath = Join-Path $InstallDir "clawmeter.exe"
$IconPath = Join-Path $InstallDir "clawmeter.ico"
$StartMenuShortcut = Join-Path ([Environment]::GetFolderPath("Programs")) "Clawmeter.lnk"
$StartupShortcut = Join-Path ([Environment]::GetFolderPath("Startup")) "Clawmeter.lnk"

function Say([string]$Message) {
    Write-Host "  $Message"
}

function Warn([string]$Message) {
    Write-Warning $Message
}

function DoStep([string]$Message, [scriptblock]$Action) {
    if ($DryRun) {
        Say "[dry-run] would $Message"
        return
    }
    & $Action
}

function Get-LatestReleaseAsset {
    $uri = "https://api.github.com/repos/$Repo/releases?per_page=5"
    $releases = Invoke-RestMethod -Uri $uri -Headers @{ "User-Agent" = "clawmeter-installer" }
    foreach ($release in $releases) {
        $asset = $release.assets | Where-Object { $_.name -eq $AssetName } | Select-Object -First 1
        if ($asset) {
            return [pscustomobject]@{
                Version = $release.tag_name
                Url = $asset.browser_download_url
            }
        }
    }
    throw "No release found with $AssetName"
}

function New-ClawmeterShortcut([string]$Path, [string]$TargetPath, [string]$Arguments) {
    $parent = Split-Path -Parent $Path
    if (-not (Test-Path $parent)) {
        New-Item -ItemType Directory -Force -Path $parent | Out-Null
    }

    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($Path)
    $shortcut.TargetPath = $TargetPath
    $shortcut.Arguments = $Arguments
    $shortcut.WorkingDirectory = Split-Path -Parent $TargetPath
    if (Test-Path $IconPath) {
        $shortcut.IconLocation = $IconPath
    }
    $shortcut.Save()
}

function Stop-Clawmeter {
    Get-Process -Name "clawmeter" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
}

function Remove-IfExists([string]$Path, [string]$Label) {
    if (Test-Path $Path) {
        DoStep "remove $Label $Path" {
            Remove-Item -Force -Recurse $Path
            Say "Removed $Label $Path"
        }
    }
}

if ($Uninstall) {
    Say "Uninstalling clawmeter..."
    DoStep "stop running clawmeter processes" { Stop-Clawmeter }
    Remove-IfExists $StartMenuShortcut "Start Menu shortcut"
    Remove-IfExists $StartupShortcut "Startup shortcut"
    Remove-IfExists $ExePath "binary"
    Remove-IfExists $IconPath "icon"
    if ((Test-Path $InstallDir) -and -not (Get-ChildItem -Force $InstallDir | Select-Object -First 1)) {
        Remove-IfExists $InstallDir "install directory"
    }
    Say "Done."
    exit 0
}

if ($DryRun) {
    Say "Dry run: no files will be written, no commands executed, no downloads made."
}

$release = Get-LatestReleaseAsset
Say "Installing clawmeter $($release.Version) (windows/amd64)..."

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("clawmeter-" + [Guid]::NewGuid().ToString("N"))
$tmpExe = Join-Path $tmp "clawmeter.exe"
$tmpIcon = Join-Path $tmp "clawmeter.ico"

DoStep "create temporary directory $tmp" {
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
}

try {
    DoStep "download $AssetName to $tmpExe" {
        Invoke-WebRequest -Uri $release.Url -OutFile $tmpExe
    }

    DoStep "create install directory $InstallDir" {
        New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    }

    DoStep "stop running clawmeter processes" {
        Stop-Clawmeter
    }

    DoStep "install binary to $ExePath" {
        Move-Item -Force $tmpExe $ExePath
    }

    $iconUrls = @(
        "https://raw.githubusercontent.com/$Repo/$($release.Version)/assets/clawmeter.ico",
        "https://raw.githubusercontent.com/$Repo/main/assets/clawmeter.ico"
    )
    $iconInstalled = $false
    foreach ($iconUrl in $iconUrls) {
        if ($DryRun) {
            Say "[dry-run] would download icon from $iconUrl"
            $iconInstalled = $true
            break
        }
        try {
            Invoke-WebRequest -Uri $iconUrl -OutFile $tmpIcon
            Move-Item -Force $tmpIcon $IconPath
            Say "Installed app icon to $IconPath"
            $iconInstalled = $true
            break
        } catch {
            Remove-Item -Force $tmpIcon -ErrorAction SilentlyContinue
        }
    }
    if (-not $iconInstalled) {
        Warn "could not install app icon; Start Menu may use the executable icon"
    }

    DoStep "create Start Menu shortcut $StartMenuShortcut" {
        New-ClawmeterShortcut -Path $StartMenuShortcut -TargetPath $ExePath -Arguments "tray"
        Say "Installed Start Menu shortcut to $StartMenuShortcut"
    }

    if ($Startup) {
        DoStep "create Startup shortcut $StartupShortcut" {
            New-ClawmeterShortcut -Path $StartupShortcut -TargetPath $ExePath -Arguments "tray"
            Say "Enabled launch-at-login with $StartupShortcut"
        }
    } else {
        Say "Launch-at-login is NOT enabled. Re-run with -Startup to enable it."
    }

    if ($Start) {
        DoStep "start clawmeter tray" {
            Start-Process -FilePath $ExePath -ArgumentList "tray" -WindowStyle Hidden
            Say "Tray started for this session."
        }
    } else {
        Say "Binary and Start Menu shortcut installed. To start the tray now, launch Clawmeter from Start Menu or run: `"$ExePath`" tray"
    }
} finally {
    if (-not $DryRun) {
        Remove-Item -Force -Recurse $tmp -ErrorAction SilentlyContinue
    }
}
