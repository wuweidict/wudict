; Copyright (C) 2026 glowinthedark
;
; SPDX-License-Identifier: GPL-3.0-or-later
;
; The Windows installer (P86 / D76). Compiled by tools\make-installer.ps1,
; which locates ISCC.exe and fills the defines below in from the built binary
; (`wudict --version`), so nothing here carries a second copy of the product
; name or the version:
;
;   .\tools\make-installer.ps1 -Exe .\wudict.exe -OutDir .\dist
;
; or `make win-installer`. Every define has a default, so opening this file in
; the Inno Setup IDE and pressing Compile also works.
;
; ONE executable is installed, not two (D76). wudict.exe is a console-subsystem
; binary that decides at runtime whether a person or a shell started it, so the
; Start-menu shortcut below opens no black window and the same file still pipes
; and reports an exit code from cmd.exe.

#ifndef AppName
  #define AppName "wuDict"
#endif
#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef NumVersion
  #define NumVersion "0.0.0"
#endif
#ifndef SourceExe
  #define SourceExe "..\..\wudict.exe"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist"
#endif
#define ProgId "wuDict.Dictionary"

[Setup]
; Durable identity. Never change this GUID: Windows keys the installed-programs
; entry off it, and a new one turns every upgrade into a second installation.
AppId={{7B267AE3-CE1C-49F4-A8B2-2DEB9F979DE3}
AppName={#AppName}
AppVersion={#AppVersion}
VersionInfoVersion={#NumVersion}
AppPublisher=glowinthedark
AppPublisherURL=https://github.com/legbehindneck/wudict
AppSupportURL=https://github.com/legbehindneck/wudict/issues
AppUpdatesURL=https://github.com/legbehindneck/wudict/releases

; Per-user, always. PrivilegesRequired=lowest means no UAC prompt, which is the
; difference between "double-click and it works" and "ask your administrator";
; with it, {autopf} resolves to %LOCALAPPDATA%\Programs, the location Microsoft
; documents for user-scoped applications. Nothing outside the user's own hive
; and profile is touched, so no architecture directives are needed either — the
; installer never sees Program Files or the WOW64 registry split.
PrivilegesRequired=lowest
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
AllowNoIcons=yes
DisableProgramGroupPage=yes

; The running server holds wudict.exe open, so an upgrade cannot replace it
; while it runs. Restart Manager is told to close it: the alternative is a
; "file in use" dialog in the middle of an install the user just asked for.
; Safe to force — SQLite's journal makes an abrupt exit recoverable by design —
; and not restarted afterwards, because the user may not have wanted it running.
CloseApplications=force
RestartApplications=no

; The PATH entry below writes to HKCU\Environment; this broadcasts the change
; so already-open Explorer-launched shells pick it up.
ChangesEnvironment=yes

SetupIconFile=wudict.ico
UninstallDisplayIcon={app}\wudict.exe,0
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes
OutputDir={#OutputDir}
OutputBaseFilename=wudict-setup-{#NumVersion}
LicenseFile=..\..\LICENSE

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon";  Description: "Create a &desktop shortcut"; Flags: unchecked
Name: "startup";      Description: "Start {#AppName} when I sign in"; Flags: unchecked
Name: "addtopath";    Description: "Add {#AppName} to my &PATH (so `wudict` works in a terminal)"
Name: "associate";    Description: "Offer {#AppName} in ""Open with"" for dictionary files"

[Files]
Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "wudict.exe"; Flags: ignoreversion
Source: "wudict.ico";   DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}";       Filename: "{app}\wudict.exe"; IconFilename: "{app}\wudict.ico"; Comment: "Serve your dictionaries in the browser"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\wudict.exe"; IconFilename: "{app}\wudict.ico"; Tasks: desktopicon
; A Startup-folder shortcut rather than a Run registry key: it is the one place
; a user can find and delete an autostart entry without a tool. --no-browser
; because a tab that opens itself at every sign-in is a nuisance; the tray icon
; is the liveness indicator there (D74).
Name: "{userstartup}\{#AppName}"; Filename: "{app}\wudict.exe"; Parameters: "--no-browser"; IconFilename: "{app}\wudict.ico"; Tasks: startup

[Registry]
; PATH. preservestringtype keeps an existing REG_SZ from being widened, and
; uninsdeletevalue is deliberately NOT used: it would delete the whole Path.
; Removal is surgical, in CurUninstallStepChanged below.
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; \
    ValueData: "{olddata};{app}"; Flags: preservestringtype; \
    Tasks: addtopath; Check: NeedsAddPath('{app}')

; File types. This registers a ProgId and adds it to each extension's
; OpenWithProgids list — it does NOT seize the default handler. That is not
; politeness: since Windows 10 the default association can only be changed by
; the user in Settings, and an installer that writes the key anyway gets its
; choice reverted and the user shown a "an app caused a problem" notice.
; Appearing in "Open with" is the whole of what an installer is allowed to do.
Root: HKCU; Subkey: "Software\Classes\{#ProgId}"; ValueType: string; ValueName: ""; \
    ValueData: "Dictionary"; Flags: uninsdeletekey; Tasks: associate
Root: HKCU; Subkey: "Software\Classes\{#ProgId}\DefaultIcon"; ValueType: string; ValueName: ""; \
    ValueData: "{app}\wudict.ico,0"; Tasks: associate
Root: HKCU; Subkey: "Software\Classes\{#ProgId}\shell\open\command"; ValueType: string; ValueName: ""; \
    ValueData: """{app}\wudict.exe"" ""%1"""; Tasks: associate

Root: HKCU; Subkey: "Software\Classes\.mdx\OpenWithProgids";  ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate
Root: HKCU; Subkey: "Software\Classes\.dsl\OpenWithProgids";  ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate
Root: HKCU; Subkey: "Software\Classes\.slob\OpenWithProgids"; ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate
Root: HKCU; Subkey: "Software\Classes\.bgl\OpenWithProgids";  ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate

[Run]
Filename: "{app}\wudict.exe"; Description: "Start {#AppName} now"; Flags: nowait postinstall skipifsilent

[Code]
const
  SHCNE_ASSOCCHANGED = $08000000;
  SHCNF_IDLIST       = $00000000;

// Declared twice on purpose: Inno's Pascal Script keeps separate import tables
// for the installer and the uninstaller, and an import without setuponly /
// uninstallonly is resolved in whichever context happens to run first.
procedure SHChangeNotify(wEventId: Integer; uFlags: Cardinal; dwItem1, dwItem2: Cardinal);
  external 'SHChangeNotify@shell32.dll stdcall setuponly';
procedure SHChangeNotifyUninst(wEventId: Integer; uFlags: Cardinal; dwItem1, dwItem2: Cardinal);
  external 'SHChangeNotify@shell32.dll stdcall uninstallonly';

// True when Dir is not already one of the semicolon-separated entries of the
// user's Path. Compared with sentinels on both ends so "C:\wu" never matches
// inside "C:\wudict".
function NeedsAddPath(Param: string): Boolean;
var
  Existing, Dir: string;
begin
  Dir := ExpandConstant(Param);
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Existing) then
  begin
    Result := True;
    Exit;
  end;
  Result := Pos(';' + Uppercase(Dir) + ';', ';' + Uppercase(Existing) + ';') = 0;
end;

procedure RemoveFromPath(const Dir: string);
var
  Existing: string;
  P: Integer;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Existing) then
    Exit;
  // Search the sentinel-wrapped copy, then index the original: the wrapper is
  // one character longer at the front, so a hit at P in the wrapper is the
  // first character of Dir at P in the original.
  P := Pos(';' + Uppercase(Dir) + ';', ';' + Uppercase(Existing) + ';');
  if P = 0 then
    Exit;
  Delete(Existing, P, Length(Dir) + 1);
  while (Length(Existing) > 0) and (Existing[Length(Existing)] = ';') do
    Delete(Existing, Length(Existing), 1);
  while (Length(Existing) > 0) and (Existing[1] = ';') do
    Delete(Existing, 1, 1);
  if Length(Existing) = 0 then
    RegDeleteValue(HKEY_CURRENT_USER, 'Environment', 'Path')
  else
    RegWriteExpandStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', Existing);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    SHChangeNotify(SHCNE_ASSOCCHANGED, SHCNF_IDLIST, 0, 0);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
  begin
    RemoveFromPath(ExpandConstant('{app}'));
    SHChangeNotifyUninst(SHCNE_ASSOCCHANGED, SHCNF_IDLIST, 0, 0);
  end;
end;
