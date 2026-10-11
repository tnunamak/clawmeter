$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
$env:LOCALAPPDATA = $root
$env:APPDATA = $root
$installDir = Join-Path $root 'Programs/Clawmeter'
New-Item -ItemType Directory -Path $installDir -Force | Out-Null
$exe = Join-Path $installDir 'clawmeter.exe'
Set-Content $exe 'existing installation'
$global:uninstallCalled = $false
try {
    function winget {
        $global:LASTEXITCODE = 0
        switch ($args[0]) {
            'show' { 'tnunamak.Clawmeter'; 'Version: 1.2.3' }
            'install' { $global:LASTEXITCODE = 7; 'download failed' }
            'uninstall' { $global:uninstallCalled = $true; Remove-Item $exe }
        }
    }
    function Get-Command {
        param($Name, $ErrorAction)
        if ($Name -eq 'winget') { [pscustomobject]@{ Source = 'winget' } }
    }
    $failed = $false
    try { & (Join-Path $PSScriptRoot 'verify-winget.ps1') -ExpectedVersion '1.2.3' } catch { $failed = $true }
    if (-not $failed) { throw 'existing installation did not stop verification' }
    if ($global:uninstallCalled -or -not (Test-Path $exe)) { throw 'failed verification removed the pre-existing installation' }
    Write-Host 'PASS pre-existing installation preserved'
} finally { Microsoft.PowerShell.Management\Remove-Item -Recurse -Force $root }
