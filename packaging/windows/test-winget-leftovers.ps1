$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot 'verify-winget.ps1'
$tokens = $null; $errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($scriptPath, [ref]$tokens, [ref]$errors)
$wait = $ast.FindAll({ param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Wait-PathGone' }, $true)[0]
Invoke-Expression $wait.Extent.Text
$uninstall = $ast.FindAll({ param($n) $n -is [Management.Automation.Language.IfStatementAst] -and $n.Extent.Text.StartsWith('if (-not $SkipUninstall)') }, $true)[0]
function Assert-True { param($Condition,$Message) if (-not $Condition) { throw "FAIL: $Message" } }
$SkipUninstall = $false
$pathPartsBefore = @()
$root = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root | Out-Null
try {
    foreach ($leftover in @('directory','shortcut','registry','none')) {
        $installDir = Join-Path $root 'installed'
        New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        $exePath = Join-Path $installDir 'clawmeter.exe'
        $startMenu = Join-Path $root 'Clawmeter.lnk'
        $uninstallKey = Join-Path $root 'registry'
        Set-Content $exePath 'exe'; Set-Content $startMenu 'shortcut'; Set-Content $uninstallKey 'key'
        $global:clockTick = 0
        function Get-Date { $global:clockTick++; [DateTime]::UtcNow.AddSeconds($global:clockTick * 100) }
        function Invoke-WinGetChecked {
            param($Message,$Arguments)
            Remove-Item $exePath
            if ($leftover -ne 'directory') { Remove-Item $installDir -Recurse -Force }
            if ($leftover -ne 'shortcut') { Remove-Item $startMenu }
            if ($leftover -ne 'registry') { Remove-Item $uninstallKey }
        }
        $failed = $false
        try { Invoke-Expression $uninstall.Extent.Text } catch { $failed = $true }
        if (($leftover -ne 'none') -ne $failed) { throw "WinGet uninstall failed to reject $leftover leftovers" }
        Microsoft.PowerShell.Management\Remove-Item $installDir,$startMenu,$uninstallKey -Recurse -Force -ErrorAction SilentlyContinue
    }
    $pathAssertion = $ast.FindAll({ param($n) $n -is [Management.Automation.Language.CommandAst] -and $n.Extent.Text.Contains('"winget uninstall removed install directory from user PATH"') }, $true)
    if ($pathAssertion.Count -ne 1) { throw 'missing install PATH removal assertion' }
    $pathPartsAfter = @($installDir.TrimEnd("\"))
    $failed = $false
    try { Invoke-Expression $pathAssertion[0].Extent.Text } catch { $failed = $true }
    if (-not $failed) { throw 'stale install PATH entry was accepted' }
    $pathPartsAfter = @('unrelated')
    Invoke-Expression $pathAssertion[0].Extent.Text
    Write-Host 'PASS WinGet directory, shortcut and registration removal checks'
} finally { Microsoft.PowerShell.Management\Remove-Item -Recurse -Force $root }
