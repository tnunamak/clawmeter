[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string[]]$Path,

    [string]$OutputDir = (Join-Path $env:TEMP ("clawmeter-defender-" + (Get-Date -Format "yyyyMMdd-HHmmss"))),

    [switch]$Scan
)

$ErrorActionPreference = "Stop"
$scanStarted = Get-Date
$out = New-Item -ItemType Directory -Force -Path $OutputDir
$resolvedPaths = @($Path | ForEach-Object { (Resolve-Path $_).Path })

function Write-JsonFile {
    param(
        [string]$Name,
        [object]$Value
    )

    $target = Join-Path $out.FullName $Name
    ConvertTo-Json -InputObject $Value -Depth 12 | Set-Content -Path $target -Encoding UTF8
}

function Command-Exists {
    param([string]$Name)
    return $null -ne (Get-Command $Name -ErrorAction SilentlyContinue)
}

$defenderCommands = [pscustomobject]@{
    StartMpScan          = Command-Exists "Start-MpScan"
    GetMpComputerStatus  = Command-Exists "Get-MpComputerStatus"
    GetMpPreference      = Command-Exists "Get-MpPreference"
    GetMpThreatDetection = Command-Exists "Get-MpThreatDetection"
}
Write-JsonFile -Name "defender-commands.json" -Value $defenderCommands

$fileEvidence = foreach ($file in $resolvedPaths) {
    $item = Get-Item $file
    $signature = if (Command-Exists "Get-AuthenticodeSignature") {
        Get-AuthenticodeSignature $file -ErrorAction SilentlyContinue
    } else {
        $null
    }
    [pscustomobject]@{
        Path                  = $item.FullName
        Length                = $item.Length
        LastWriteTimeUtc      = $item.LastWriteTimeUtc.ToString("o")
        SHA256                = (Get-FileHash $file -Algorithm SHA256).Hash
        SHA1                  = (Get-FileHash $file -Algorithm SHA1).Hash
        AuthenticodeStatus    = if ($signature) { $signature.Status.ToString() } else { "Unavailable" }
        AuthenticodeSigner    = if ($signature.SignerCertificate) { $signature.SignerCertificate.Subject } else { $null }
        AuthenticodeTimestamp = if ($signature.TimeStamperCertificate) { $signature.TimeStamperCertificate.Subject } else { $null }
    }
}
Write-JsonFile -Name "files.json" -Value $fileEvidence

if ($defenderCommands.GetMpComputerStatus) {
    try {
        Write-JsonFile -Name "defender-status.json" -Value (Get-MpComputerStatus)
    } catch {
        Write-JsonFile -Name "defender-status-error.json" -Value ([pscustomobject]@{ Error = $_.Exception.Message })
    }
}

if ($defenderCommands.GetMpPreference) {
    try {
        Write-JsonFile -Name "defender-preference.json" -Value (Get-MpPreference)
    } catch {
        Write-JsonFile -Name "defender-preference-error.json" -Value ([pscustomobject]@{ Error = $_.Exception.Message })
    }
}

if ($Scan) {
    if (-not $defenderCommands.StartMpScan) {
        Write-Warning "Start-MpScan is not available on this machine."
    } else {
        foreach ($file in $resolvedPaths) {
            try {
                Write-Host "Scanning $file with Microsoft Defender..."
                Start-MpScan -ScanType CustomScan -ScanPath $file
            } catch {
                Write-JsonFile -Name "defender-scan-error.json" -Value ([pscustomobject]@{
                    Path  = $file
                    Error = $_.Exception.Message
                })
            }
        }
    }
}

$detections = @()
if ($defenderCommands.GetMpThreatDetection) {
    try {
        $allDetections = @(Get-MpThreatDetection)
        foreach ($detection in $allDetections) {
            $json = $detection | ConvertTo-Json -Depth 12 -Compress
            foreach ($file in $resolvedPaths) {
                if ($json.IndexOf($file, [System.StringComparison]::OrdinalIgnoreCase) -ge 0) {
                    $detections += $detection
                    break
                }
            }
        }
    } catch {
        Write-JsonFile -Name "defender-detections-error.json" -Value ([pscustomobject]@{ Error = $_.Exception.Message })
    }
}
Write-JsonFile -Name "defender-detections-matching-files.json" -Value $detections

$readme = @"
Clawmeter Defender evidence
Generated: $($scanStarted.ToString("o"))

Files inspected:
$($resolvedPaths -join "`r`n")

If Microsoft Defender or Windows Security incorrectly detected Clawmeter, submit the flagged file to Microsoft:
https://www.microsoft.com/en-us/wdsi/filesubmission

Use the software developer / false-positive path, attach the exact flagged file, and include:
- SHA256 from files.json
- Detection name from defender-detections-matching-files.json or Windows Security Protection history
- Download URL from the GitHub release
- Statement: Clawmeter is an open-source MIT-licensed local quota meter for AI coding tools. It reads existing provider credentials locally, contacts provider APIs for quota status, and has no Clawmeter-operated telemetry service.

Do not publish this folder publicly without reviewing it. Defender status and preference files can include local machine policy details.
"@

Set-Content -Path (Join-Path $out.FullName "README.txt") -Value $readme -Encoding UTF8
Write-Host "Defender evidence written to $($out.FullName)"
