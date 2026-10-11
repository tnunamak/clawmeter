param([switch]$SkipShortcut)
$ErrorActionPreference = 'Stop'
function Assert-True { param([bool]$Condition, [string]$Message) if (-not $Condition) { throw $Message } }
function Test-Path { return $true }
foreach ($name in @('verify-installer.ps1', 'verify-winget.ps1')) {
    $tokens = $null; $errors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $name), [ref]$tokens, [ref]$errors)
    $assertions = @($ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.CommandAst] -and $node.GetCommandName() -eq 'Assert-True' }, $true))
    $exePath = 'C:\Programs\Clawmeter\clawmeter.exe'
    $startMenu = 'fixture.lnk'
    if (-not $SkipShortcut) {
    foreach ($bad in @([pscustomobject]@{ TargetPath = 'wrong.exe'; Arguments = 'tray' }, [pscustomobject]@{ TargetPath = $exePath; Arguments = '--bad' })) {
        $shortcut = $bad
        $failed = $false
        try {
            foreach ($command in $assertions | Where-Object { $_.Extent.Text -match 'shortcut' }) { Invoke-Expression $command.Extent.Text }
        } catch { $failed = $true }
        if (-not $failed) { throw "$name accepted an incorrect shortcut target or arguments" }
    }
    }
    $installDir = 'C:\Programs\Clawmeter'
    $pathPartsBefore = @('C:\keep-a', 'C:\keep-b')
    $pathPartsAfter = @('C:\keep-a')
    $failed = $false
    try {
        foreach ($command in $assertions | Where-Object { $_.Extent.Text -match 'preserved existing user PATH' }) { Invoke-Expression $command.Extent.Text }
    } catch { $failed = $true }
    if (-not $failed) { throw "$name accepted deletion of an unrelated PATH entry" }
}
Write-Host 'PASS shortcut and PATH preservation verifier regressions'
