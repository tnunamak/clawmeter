$ErrorActionPreference = 'Stop'
$root = Join-Path ([System.IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root | Out-Null
$fixturePath = 'C:\temp\ClawmeterSetup.exe'
function Resolve-Path { param($Path) [pscustomobject]@{ Path = $fixturePath } }
function Get-Item { param($Path) [pscustomobject]@{ FullName = $fixturePath; Length = 1; LastWriteTimeUtc = [DateTime]::UtcNow } }
function Get-FileHash { param($Path, $Algorithm) [pscustomobject]@{ Hash = 'FAKE' } }
function Get-MpThreatDetection { @(
  [pscustomobject]@{ ThreatID = 1; Resources = @("file:_$fixturePath") },
  [pscustomobject]@{ ThreatID = 2; Resources = @('file:_C:\unrelated\Other.exe') }
) }
try {
  & (Join-Path $PSScriptRoot 'collect-defender-evidence.ps1') -Path $fixturePath -OutputDir $root
  $matches = @(Get-Content (Join-Path $root 'defender-detections-matching-files.json') -Raw | ConvertFrom-Json)
  if ($matches.Count -ne 1 -or $matches[0].ThreatID -ne 1) { throw 'expected exactly the detection with the raw Windows path' }
  Write-Host 'PASS Defender retains matching escaped Windows paths and excludes unrelated paths'
} finally { Remove-Item -Recurse -Force $root }
