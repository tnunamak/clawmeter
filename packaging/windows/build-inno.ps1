[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$BinaryPath,

    [string]$Version = "0.0.0-dev",

    [string]$OutputDir = (Join-Path (Get-Location) "dist\windows"),

    [string]$CompilerPath
)

$ErrorActionPreference = "Stop"

function Resolve-InnoCompiler {
    param([string]$RequestedPath)

    if ($RequestedPath) {
        if (!(Test-Path $RequestedPath)) {
            throw "Inno Setup compiler not found at $RequestedPath"
        }
        return (Resolve-Path $RequestedPath).Path
    }

    $candidates = @()
    if (${env:ProgramFiles(x86)}) {
        $candidates += (Join-Path ${env:ProgramFiles(x86)} "Inno Setup 6\ISCC.exe")
    }
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles "Inno Setup 6\ISCC.exe")
    }

    foreach ($candidate in $candidates) {
        if ($candidate -and (Test-Path $candidate)) {
            return (Resolve-Path $candidate).Path
        }
    }

    $command = Get-Command ISCC.exe -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }

    throw "Inno Setup compiler not found. Install Inno Setup 6, then rerun this script."
}

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$binary = Resolve-Path $BinaryPath
$icon = Resolve-Path (Join-Path $repoRoot "assets\clawmeter.ico")
$script = Resolve-Path (Join-Path $PSScriptRoot "clawmeter.iss")
$compiler = Resolve-InnoCompiler -RequestedPath $CompilerPath
$versionValue = $Version.TrimStart("v")
$stage = Join-Path $repoRoot "tmp\inno-stage"
$out = New-Item -ItemType Directory -Force -Path $OutputDir

Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $stage | Out-Null
Copy-Item -Force $binary (Join-Path $stage "clawmeter.exe")
Copy-Item -Force $icon (Join-Path $stage "clawmeter.ico")

& $compiler `
    "/DAppVersion=$versionValue" `
    "/DSourceDir=$stage" `
    "/DOutputDir=$($out.FullName)" `
    $script

if ($LASTEXITCODE -ne 0) {
    throw "Inno Setup compiler exited with $LASTEXITCODE"
}

$setup = Join-Path $out.FullName "ClawmeterSetup.exe"
if (!(Test-Path $setup)) {
    throw "Expected installer was not produced: $setup"
}

Write-Host "Built $setup"
