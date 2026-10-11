param([switch]$SkipDirectory)
$ErrorActionPreference = 'Stop'
$root = Join-Path ([System.IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root | Out-Null
try {
 if (-not $SkipDirectory) {
 foreach ($name in @('verify-installer.ps1', 'verify-winget.ps1')) {
  $tokens = $null; $errors = $null
  $ast = [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $name), [ref]$tokens, [ref]$errors)
  $func = $ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Wait-PathGone' }, $true)[0]
  Invoke-Expression $func.Extent.Text
  $failed = $false
  try { Wait-PathGone -Path $root -TimeoutSeconds 0 } catch { $failed = $true }
  if (-not $failed) { throw "$name accepted a leftover directory after timeout" }
 }
 }
 $tokens = $null; $errors = $null
 $ast = [System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'verify-winget.ps1'), [ref]$tokens, [ref]$errors)
 function Assert-True { param([bool]$Condition, [string]$Message) if (-not $Condition) { throw $Message } }
 $blocks = @($ast.FindAll({ param($node) $node -is [System.Management.Automation.Language.IfStatementAst] -and $node.Extent.Text -match 'expected version' }, $true))
 $ExpectedVersion = '1.2.3'
 foreach ($actual in @('1.2.30', '11.2.3', '1.2.3-beta')) {
  $showOutput = "Version: $actual"
  $versionOutput = "clawmeter $actual"
  foreach ($block in $blocks) {
   $failed = $false
   try { Invoke-Expression $block.Extent.Text } catch { $failed = $true }
   if (-not $failed) { throw "verifier accepted wrong version $actual" }
  }
 }
 $showOutput = "Version: 1.2.3`n"; $versionOutput = "clawmeter v1.2.3`n"
 foreach ($block in $blocks) { Invoke-Expression $block.Extent.Text }
 Write-Host 'PASS leftover-directory and exact-version verifier regressions'
} finally { Remove-Item -Recurse -Force $root }
