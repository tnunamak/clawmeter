param([switch]$SkipInstaller)
$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root | Out-Null
$env:LOCALAPPDATA = $root
$env:APPDATA = $root
$installer = Join-Path $root 'installer.exe'
Set-Content $installer 'fixture'
$global:fixtureUninstaller = Join-Path $root 'uninstall.ps1'
$marker = Join-Path $root 'removed'
Set-Content $global:fixtureUninstaller "Set-Content '$marker' 'removed'; `$global:LASTEXITCODE = 0"
try {
    $global:installed = $false
    $global:capturedArguments = @()
    function Get-ChildItem {
        if ($global:installed -and -not $global:noUninstaller) { [pscustomobject]@{ FullName = $global:fixtureUninstaller; LastWriteTime = Get-Date } }
    }
    function Remove-ItemProperty { }
    function Start-Process {
        param($FilePath, $ArgumentList, [switch]$Wait, [switch]$PassThru)
        $global:installed = $true
        $global:capturedArguments = $ArgumentList
        [pscustomobject]@{ ExitCode = 0 }
    }
    if (-not $SkipInstaller) {
    $failed = $false
    try { & (Join-Path $PSScriptRoot 'verify-installer.ps1') -InstallerPath $installer } catch { $failed = $true }
    if (-not $failed) { throw 'missing executable did not fail installer verification' }
    if (-not (Test-Path $marker)) { throw 'installer assertion failure left the installed fixture behind' }
    if ($global:capturedArguments -match '/TASKS=') { throw 'default verification overrode installer task defaults' }
    Remove-Item $marker

    $global:installed = $false
    $global:noUninstaller = $true
    $partialInstall = Join-Path $root 'Programs\Clawmeter'
    function Start-Process {
        param($FilePath, $ArgumentList, [switch]$Wait, [switch]$PassThru)
        $global:installed = $true
        New-Item -ItemType Directory -Force $partialInstall | Out-Null
        [pscustomobject]@{ ExitCode = 1 }
    }
    $failed = $false
    try { & (Join-Path $PSScriptRoot 'verify-installer.ps1') -InstallerPath $installer -WarningAction SilentlyContinue } catch { $failed = $true }
    if (-not $failed) { throw 'failed installer did not fail verification' }
    if (Test-Path $partialInstall) { throw 'partial installation without an uninstaller was left behind' }
    $global:noUninstaller = $false
    }

    function winget {
        $global:LASTEXITCODE = 0
        switch ($args[0]) {
            'show' { 'tnunamak.Clawmeter'; 'Version: 1.2.3' }
            'uninstall' { Set-Content $marker 'removed' }
        }
    }
    function Get-Command {
        param($Name, $ErrorAction)
        if ($Name -eq 'winget') { [pscustomobject]@{ Source = 'winget' } }
    }
    $failed = $false
    try { & (Join-Path $PSScriptRoot 'verify-winget.ps1') -ExpectedVersion '1.2.3' } catch { $failed = $true }
    if (-not $failed) { throw 'missing executable did not fail WinGet verification' }
    if (-not (Test-Path $marker)) { throw 'WinGet assertion failure did not attempt uninstall' }
    Write-Host 'PASS verifier assertion cleanup and default-task regression'
} finally {
    Microsoft.PowerShell.Management\Remove-Item -Recurse -Force $root
}
