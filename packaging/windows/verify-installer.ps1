[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$InstallerPath,

    [switch]$IncludeStartup,

    [switch]$ExpectSigned,

    [string]$ExpectedPublisher = "Tim Nunamaker"
)

$ErrorActionPreference = "Stop"

function Assert-True {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw "FAIL: $Message"
    }

    Write-Host "PASS: $Message"
}

function Assert-NotExists {
    param(
        [string]$Path,
        [string]$Message
    )

    Assert-True -Condition (-not (Test-Path $Path)) -Message $Message
}

function Assert-Signed {
    param([string]$Path)

    $signature = Get-AuthenticodeSignature $Path
    Assert-True -Condition ($signature.Status -eq "Valid") -Message "$Path has a valid Authenticode signature"
    Assert-True -Condition ($signature.SignerCertificate.Subject -like "*$ExpectedPublisher*") -Message "$Path signer contains $ExpectedPublisher"
}

function Wait-PathGone {
    param(
        [string]$Path,
        [int]$TimeoutSeconds = 20
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Test-Path $Path) -and ((Get-Date) -lt $deadline)) {
        Start-Sleep -Milliseconds 500
    }
}

$installer = (Resolve-Path $InstallerPath).Path
$installDir = Join-Path $env:LOCALAPPDATA "Programs\Clawmeter"
$exePath = Join-Path $installDir "clawmeter.exe"
$uninstaller = Join-Path $installDir "unins000.exe"
$startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\Clawmeter\Clawmeter.lnk"
$runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
$runValue = "Clawmeter"
$uninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{92EEFACA-DA48-4099-937D-F16E591F1DEE}_is1"

if ($ExpectSigned) {
    Assert-Signed -Path $installer
}

if (Test-Path $uninstaller) {
    & $uninstaller /VERYSILENT /SUPPRESSMSGBOXES /NORESTART
    Wait-PathGone -Path $exePath
}

Remove-Item -Recurse -Force $installDir -ErrorAction SilentlyContinue
Remove-Item -Force $startMenu -ErrorAction SilentlyContinue
Remove-ItemProperty -Path $runKey -Name $runValue -ErrorAction SilentlyContinue

$tasks = "addtopath"
if ($IncludeStartup) {
    $tasks = "addtopath,startup"
}

$arguments = @(
    "/VERYSILENT",
    "/SUPPRESSMSGBOXES",
    "/NORESTART",
    "/TASKS=$tasks"
)

$process = Start-Process -FilePath $installer -ArgumentList $arguments -Wait -PassThru
Assert-True -Condition ($process.ExitCode -eq 0) -Message "installer exited 0"
Assert-True -Condition (Test-Path $exePath) -Message "installed clawmeter.exe"
Assert-True -Condition (Test-Path $uninstaller) -Message "installed uninstaller"
Assert-True -Condition (Test-Path $startMenu) -Message "created Start Menu shortcut"
Assert-True -Condition (Test-Path $uninstallKey) -Message "created uninstall registry entry"

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($null -eq $userPath) {
    $userPath = ""
}
$pathParts = @($userPath -split ";" | ForEach-Object { $_.TrimEnd("\") })
Assert-True -Condition ($pathParts -contains $installDir.TrimEnd("\")) -Message "added install directory to user PATH"

if ($IncludeStartup) {
    $startup = (Get-ItemProperty -Path $runKey -Name $runValue -ErrorAction SilentlyContinue).$runValue
    Assert-True -Condition ($startup -like "*clawmeter.exe* tray*") -Message "created launch-at-login registry value"
}

& $exePath providers | Out-String | Write-Host
Assert-True -Condition ($LASTEXITCODE -eq 0) -Message "installed CLI runs providers"

if ($ExpectSigned) {
    Assert-Signed -Path $exePath
    Assert-Signed -Path $uninstaller
}

& $uninstaller /VERYSILENT /SUPPRESSMSGBOXES /NORESTART
Assert-True -Condition ($LASTEXITCODE -eq 0) -Message "uninstaller exited 0"
Wait-PathGone -Path $exePath
Wait-PathGone -Path $startMenu
Assert-NotExists -Path $exePath -Message "removed installed clawmeter.exe"
Assert-NotExists -Path $startMenu -Message "removed Start Menu shortcut"

$startupAfter = (Get-ItemProperty -Path $runKey -Name $runValue -ErrorAction SilentlyContinue).$runValue
Assert-True -Condition ($null -eq $startupAfter) -Message "removed launch-at-login registry value"

$userPathAfter = [Environment]::GetEnvironmentVariable("Path", "User")
if ($null -eq $userPathAfter) {
    $userPathAfter = ""
}
$pathPartsAfter = @($userPathAfter -split ";" | ForEach-Object { $_.TrimEnd("\") })
Assert-True -Condition (-not ($pathPartsAfter -contains $installDir.TrimEnd("\"))) -Message "removed install directory from user PATH"
