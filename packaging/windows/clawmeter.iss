#define AppName "Clawmeter"
#ifndef AppVersion
#define AppVersion "0.0.0-dev"
#endif
#ifndef SourceDir
#define SourceDir "."
#endif
#ifndef OutputDir
#define OutputDir "."
#endif

[Setup]
AppId={{92EEFACA-DA48-4099-937D-F16E591F1DEE}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=Tim Nunamaker
AppPublisherURL=https://github.com/tnunamak
AppSupportURL=https://github.com/tnunamak/clawmeter/issues
AppUpdatesURL=https://github.com/tnunamak/clawmeter/releases
DefaultDirName={localappdata}\Programs\Clawmeter
DefaultGroupName=Clawmeter
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
SetupIconFile={#SourceDir}\clawmeter.ico
UninstallDisplayIcon={app}\clawmeter.ico
InfoBeforeFile={#SourceDir}\PRIVACY.md
OutputDir={#OutputDir}
OutputBaseFilename=ClawmeterSetup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ChangesEnvironment=yes

#ifdef AppSignTool
SignTool={#AppSignTool}
SignToolRunMinimized=yes
SignToolRetryCount=3
SignedUninstaller=yes
#endif

[Tasks]
Name: "addtopath"; Description: "Add Clawmeter to my PATH"; Flags: checkedonce
Name: "updates"; Description: "Check for Clawmeter updates automatically"; Flags: checkedonce
Name: "startup"; Description: "Start Clawmeter when I sign in"; Flags: unchecked

[Files]
#ifdef AppSignTool
Source: "{#SourceDir}\clawmeter.exe"; DestDir: "{app}"; Flags: ignoreversion signonce
#else
Source: "{#SourceDir}\clawmeter.exe"; DestDir: "{app}"; Flags: ignoreversion
#endif
Source: "{#SourceDir}\clawmeter.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Clawmeter"; Filename: "{app}\clawmeter.exe"; Parameters: "tray"; IconFilename: "{app}\clawmeter.ico"
Name: "{group}\Uninstall Clawmeter"; Filename: "{uninstallexe}"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "Clawmeter"; ValueData: """{app}\clawmeter.exe"" tray"; Flags: uninsdeletevalue; Tasks: startup

[Run]
Filename: "{app}\clawmeter.exe"; Parameters: "config set check_for_updates false"; Flags: runhidden; Check: not WizardIsTaskSelected('updates')
Filename: "{app}\clawmeter.exe"; Parameters: "tray"; Description: "Launch Clawmeter"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{cmd}"; Parameters: "/C taskkill /IM clawmeter.exe /F >NUL 2>NUL"; Flags: runhidden

[Code]
function NormalizePathEntry(Value: string): string;
begin
  Result := Lowercase(RemoveBackslashUnlessRoot(ExpandConstant(Value)));
end;

function PathContainsEntry(PathValue, Entry: string): Boolean;
var
  Needle: string;
  Haystack: string;
begin
  Needle := ';' + NormalizePathEntry(Entry) + ';';
  Haystack := ';' + Lowercase(PathValue) + ';';
  Result := Pos(Needle, Haystack) > 0;
end;

procedure AddClawmeterToPath();
var
  PathValue: string;
  AppDir: string;
begin
  AppDir := ExpandConstant('{app}');
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', PathValue) then
    PathValue := '';

  if not PathContainsEntry(PathValue, AppDir) then begin
    if Length(PathValue) = 0 then
      PathValue := AppDir
    else
      PathValue := PathValue + ';' + AppDir;
    RegWriteStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', PathValue);
  end;
end;

procedure RemoveClawmeterFromPath();
var
  PathValue: string;
  AppDir: string;
begin
  AppDir := ExpandConstant('{app}');
  if RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', PathValue) then begin
    StringChangeEx(PathValue, AppDir + ';', '', True);
    StringChangeEx(PathValue, ';' + AppDir, '', True);
    if CompareText(PathValue, AppDir) = 0 then
      PathValue := '';
    RegWriteStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', PathValue);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then begin
    if WizardIsTaskSelected('addtopath') then
      AddClawmeterToPath();
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    RemoveClawmeterFromPath();
end;
