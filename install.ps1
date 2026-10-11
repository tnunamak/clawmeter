[CmdletBinding()]
param(
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\Clawmeter"),
    [string]$LocalBinary,
    [switch]$Start,
    [switch]$Startup,
    [switch]$Uninstall,
    [switch]$NoModifyPath,
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
    $headers = @{ "User-Agent" = "clawmeter-installer" }
    $token = if ($env:GITHUB_TOKEN) { $env:GITHUB_TOKEN } else { $env:GH_TOKEN }
    if ($token) { $headers.Authorization = "Bearer $token" }
    try {
        $releases = Invoke-RestMethod -Uri $uri -Headers $headers -MaximumRedirection 0
    } catch {
        Say "GitHub API unavailable; falling back to releases/latest."
        $location = $null
        try {
            $response = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" -Method Head -MaximumRedirection 0
            $location = $response.Headers['Location']
        } catch {
            if ($_.Exception.Response) {
                $location = $_.Exception.Response.Headers.Location
                if (-not $location) {
                    $location = $_.Exception.Response.Headers['Location']
                }
            }
        }
        $match = [regex]::Match([string]$location, '^https://github\.com/tnunamak/clawmeter/releases/tag/(v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$')
        if (-not $match.Success) {
            throw "Latest release redirect did not contain a valid version tag"
        }
        $tag = $match.Groups[1].Value
        return [pscustomobject]@{
            Version = $tag
            Url = "https://github.com/$Repo/releases/download/$tag/$AssetName"
            SumsUrl = "https://github.com/$Repo/releases/download/$tag/SHA256SUMS.txt"
        }
    }
    foreach ($release in $releases) {
        $asset = $release.assets | Where-Object { $_.name -eq $AssetName } | Select-Object -First 1
        if ($asset) {
            $sums = @($release.assets | Where-Object { $_.name -ceq "SHA256SUMS.txt" })
            if ($sums.Count -ne 1) {
                throw "Release $($release.tag_name) must contain exactly one SHA256SUMS.txt asset"
            }
            return [pscustomobject]@{
                Version = $release.tag_name
                Url = $asset.browser_download_url
                SumsUrl = $sums[0].browser_download_url
            }
        }
    }
    throw "No release found with $AssetName"
}

function Assert-ArtifactChecksum([string]$BinaryPath, [string]$SumsPath) {
    $entries = @(Get-Content -LiteralPath $SumsPath | Where-Object {
        $fields = $_.Trim() -split '\s+'
        $fields.Count -ge 2 -and ($fields[1] -replace '^\*', '') -ceq $AssetName
    })
    if ($entries.Count -ne 1) {
        throw "Expected exactly one checksum for $AssetName"
    }
    $fields = $entries[0].Trim() -split '\s+'
    if ($fields.Count -ne 2 -or $fields[0] -cnotmatch '^[0-9a-fA-F]{64}$') {
        throw "Invalid SHA-256 checksum for $AssetName"
    }
    $actual = (Get-FileHash -LiteralPath $BinaryPath -Algorithm SHA256).Hash
    if ($actual -ine $fields[0]) {
        throw "SHA-256 mismatch for $AssetName; existing binary was not changed"
    }
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

# PATH-modification helpers.
# We edit the User PATH via the .NET Environment API rather than `setx`,
# because `setx` silently truncates at 1024 characters. We then broadcast
# WM_SETTINGCHANGE so already-open shells pick the change up.
$Script:PathBroadcasterAdded = $false
function Add-PathBroadcasterType {
    if ($Script:PathBroadcasterAdded) { return }
    $signature = @'
using System;
using System.Runtime.InteropServices;
public static class ClawmeterPathBroadcaster {
    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
    private static extern IntPtr SendMessageTimeout(
        IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam,
        uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
    public static void Broadcast() {
        UIntPtr result;
        SendMessageTimeout((IntPtr)0xffff, 0x1A, UIntPtr.Zero, "Environment",
            0x2, 5000, out result);
    }
}
'@
    Add-Type -TypeDefinition $signature -ErrorAction SilentlyContinue | Out-Null
    $Script:PathBroadcasterAdded = $true
}

function Get-UserPathEntries {
    $raw = [Environment]::GetEnvironmentVariable("Path", "User")
    if ([string]::IsNullOrWhiteSpace($raw)) {
        return @()
    }
    return $raw.Split(';') | Where-Object { $_ -ne "" }
}

function Set-UserPath([string[]]$Entries) {
    $value = ($Entries -join ';')
    [Environment]::SetEnvironmentVariable("Path", $value, "User")
    Add-PathBroadcasterType
    try { [ClawmeterPathBroadcaster]::Broadcast() } catch { }
}

function Test-PathContains([string[]]$Entries, [string]$Candidate) {
    $normalized = $Candidate.TrimEnd('\').ToLowerInvariant()
    foreach ($entry in $Entries) {
        if ($entry.TrimEnd('\').ToLowerInvariant() -eq $normalized) {
            return $true
        }
    }
    return $false
}

function Add-UserPathEntry([string]$Dir) {
    $entries = Get-UserPathEntries
    if (Test-PathContains $entries $Dir) {
        Say "PATH already contains $Dir"
        return
    }
    $newEntries = @($entries) + $Dir
    DoStep "add $Dir to user PATH" {
        Set-UserPath $newEntries
        Say "Added $Dir to user PATH (open a new terminal to use ``clawmeter``)"
    }.GetNewClosure()
}

function Remove-UserPathEntry([string]$Dir) {
    $entries = Get-UserPathEntries
    if (-not (Test-PathContains $entries $Dir)) {
        return
    }
    $normalized = $Dir.TrimEnd('\').ToLowerInvariant()
    $filtered = @($entries | Where-Object { $_.TrimEnd('\').ToLowerInvariant() -ne $normalized })
    DoStep "remove $Dir from user PATH" {
        Set-UserPath $filtered
        Say "Removed $Dir from user PATH"
    }.GetNewClosure()
}

if ($Uninstall) {
    Say "Uninstalling clawmeter..."
    # Disable launch-at-login before removing the binary, so the binary's
    # own autostart cleanup (registry edit) runs while it's still on disk.
    # We also remove the legacy .lnk that older installers left behind,
    # for users upgrading from before the registry-based autostart.
    if (Test-Path $ExePath) {
        DoStep "disable launch at login" {
            & $ExePath tray --uninstall | Out-Null
        }
    }
    Remove-IfExists $StartupShortcut "legacy Startup shortcut"
    DoStep "stop running clawmeter processes" { Stop-Clawmeter }
    Remove-IfExists $StartMenuShortcut "Start Menu shortcut"
    Remove-IfExists $ExePath "binary"
    Remove-IfExists $IconPath "icon"
    if (-not $NoModifyPath) {
        Remove-UserPathEntry $InstallDir
    }
    if ((Test-Path $InstallDir) -and -not (Get-ChildItem -Force $InstallDir | Select-Object -First 1)) {
        Remove-IfExists $InstallDir "install directory"
    }
    Say "Done."
    exit 0
}

if ($DryRun) {
    Say "Dry run: no files will be written, no commands executed, no downloads made."
}

if ($LocalBinary) {
    if (-not (Test-Path $LocalBinary)) {
        throw "Local binary not found: $LocalBinary"
    }
    Say "Installing clawmeter from local binary $LocalBinary (windows/amd64)..."
    $release = [pscustomobject]@{ Version = "local"; Url = $null }
} elseif ($DryRun) {
    $release = [pscustomobject]@{ Version = "latest"; Url = "(release asset)"; SumsUrl = "(release checksums)" }
    Say "[dry-run] would find the latest release containing $AssetName and SHA256SUMS.txt"
} else {
    $release = Get-LatestReleaseAsset
    Say "Installing clawmeter $($release.Version) (windows/amd64)..."
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("clawmeter-" + [Guid]::NewGuid().ToString("N"))
$tmpExe = Join-Path $tmp "clawmeter.exe"
$tmpIcon = Join-Path $tmp "clawmeter.ico"
$tmpSums = Join-Path $tmp "SHA256SUMS.txt"

DoStep "create temporary directory $tmp" {
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
}

try {
    if ($LocalBinary) {
        DoStep "copy $LocalBinary to $tmpExe" {
            Copy-Item -Force $LocalBinary $tmpExe
        }
    } else {
        DoStep "download and verify $AssetName against its release checksums" {
            Invoke-WebRequest -Uri $release.SumsUrl -OutFile $tmpSums
            Invoke-WebRequest -Uri $release.Url -OutFile $tmpExe
            Assert-ArtifactChecksum -BinaryPath $tmpExe -SumsPath $tmpSums
        }
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

    if ($NoModifyPath) {
        Say "Skipping PATH modification (-NoModifyPath)."
    } else {
        Add-UserPathEntry $InstallDir
    }

    if ($LocalBinary) {
        $iconUrls = @("https://raw.githubusercontent.com/$Repo/main/assets/clawmeter.ico")
    } else {
        $iconUrls = @(
            "https://raw.githubusercontent.com/$Repo/$($release.Version)/assets/clawmeter.ico",
            "https://raw.githubusercontent.com/$Repo/main/assets/clawmeter.ico"
        )
    }
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
        DoStep "enable launch at login" {
            # Defer to the binary's own autostart logic (registry Run key)
            # so this matches what the tray's "Launch at login" toggle does.
            # Single mechanism, two entry points.
            & $ExePath tray --install | Out-Null
            Say "Enabled launch-at-login"
        }
    } else {
        Say "Launch-at-login is NOT enabled. Re-run with -Startup to enable it."
    }

    if ($Start) {
        DoStep "start clawmeter tray" {
            $proc = Start-Process -FilePath $ExePath -ArgumentList "tray" -WindowStyle Hidden -PassThru
            Start-Sleep -Seconds 5
            if ($proc.HasExited) {
                throw "tray exited after launch; run `"$ExePath`" tray from PowerShell to see the error"
            }
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
