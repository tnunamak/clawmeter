[CmdletBinding()]
param(
    [string]$Version = "0.0.0-dev",

    [string]$IconPath = "assets\clawmeter.ico",

    [string]$OutputPath = "cmd\clawmeter\rsrc_windows_amd64.syso",

    [string]$WindresPath
)

$ErrorActionPreference = "Stop"

function Resolve-Windres {
    param([string]$RequestedPath)

    if ($RequestedPath) {
        if (!(Test-Path $RequestedPath)) {
            throw "windres not found at $RequestedPath"
        }
        return (Resolve-Path $RequestedPath).Path
    }

    $candidatePaths = @(
        "windres.exe",
        "x86_64-w64-mingw32-windres.exe",
        "windres",
        "$env:MSYSTEM_PREFIX\bin\windres.exe",
        "C:\msys64\ucrt64\bin\windres.exe",
        "C:\msys64\mingw64\bin\windres.exe",
        "C:\msys64\clang64\bin\llvm-windres.exe"
    )

    foreach ($name in $candidatePaths) {
        $command = Get-Command $name -ErrorAction SilentlyContinue
        if ($command) {
            return $command.Source
        }
        if (Test-Path $name) {
            return (Resolve-Path $name).Path
        }
    }

    throw "windres not found. Install MinGW windres or provide -WindresPath."
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$icon = (Resolve-Path (Join-Path $repoRoot $IconPath)).Path
$output = Join-Path $repoRoot $OutputPath
$windres = Resolve-Windres -RequestedPath $WindresPath
$versionValue = $Version.TrimStart("v")

$numericParts = @($versionValue -split "[^0-9]+" | Where-Object { $_ -ne "" } | Select-Object -First 4)
while ($numericParts.Count -lt 4) {
    $numericParts += "0"
}
$numericVersion = ($numericParts | ForEach-Object { [int]$_ }) -join ","

$escapedIcon = $icon.Replace("\", "\\")
$rc = @"
1 ICON "$escapedIcon"

1 VERSIONINFO
FILEVERSION $numericVersion
PRODUCTVERSION $numericVersion
FILEFLAGSMASK 0x3fL
FILEFLAGS 0x0L
FILEOS 0x40004L
FILETYPE 0x1L
FILESUBTYPE 0x0L
BEGIN
  BLOCK "StringFileInfo"
  BEGIN
    BLOCK "040904b0"
    BEGIN
      VALUE "CompanyName", "Tim Nunamaker\0"
      VALUE "FileDescription", "Clawmeter system tray and CLI quota meter\0"
      VALUE "FileVersion", "$versionValue\0"
      VALUE "InternalName", "clawmeter.exe\0"
      VALUE "OriginalFilename", "clawmeter.exe\0"
      VALUE "ProductName", "Clawmeter\0"
      VALUE "ProductVersion", "$versionValue\0"
      VALUE "LegalCopyright", "Copyright Tim Nunamaker\0"
    END
  END
  BLOCK "VarFileInfo"
  BEGIN
    VALUE "Translation", 0x0409, 1200
  END
END
"@

$tempRc = Join-Path ([System.IO.Path]::GetTempPath()) ("clawmeter-version-" + [System.Guid]::NewGuid().ToString("N") + ".rc")
try {
    Set-Content -Path $tempRc -Value $rc -Encoding ASCII
    New-Item -ItemType Directory -Force -Path (Split-Path $output -Parent) | Out-Null
    & $windres -O coff -o $output $tempRc
    if ($LASTEXITCODE -ne 0) {
        throw "windres exited with $LASTEXITCODE"
    }
    Write-Host "Built $output"
}
finally {
    Remove-Item -Force $tempRc -ErrorAction SilentlyContinue
}
