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
AppPublisher=legbehindneck
AppPublisherURL=https://github.com/legbehindneck/wudict
AppSupportURL=https://github.com/legbehindneck/wudict/issues
AppUpdatesURL=https://github.com/legbehindneck/wudict/releases

; Both install modes, all users first. PrivilegesRequired is where the mode
; dialog starts, so admin here makes "Install for all users" the preselected
; answer and leaves "Install for me only" one click away — and that second mode
; still asks for no administrator password and still puts everything in the
; user's own profile, exactly as this installer did before. Allowing the dialog
; also allows /ALLUSERS and /CURRENTUSER for scripted installs, and
; UsePreviousPrivileges (no by default) makes an upgrade reuse whichever mode
; the first install chose instead of asking again.
;
; Use Inno Setup's built-in privilege-selection dialog. The base value must be
; "admin" for the dialog to offer both installation modes. Setting
; PrivilegesRequired=lowest would force a per-user installation and suppress
; the all-users choice.
;
; /ALLUSERS and /CURRENTUSER can also be used for scripted installations.
; UsePreviousPrivileges=no makes Setup ask again instead of silently reusing
; the mode selected by an earlier installation.
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog
UsePreviousPrivileges=no
DefaultDirName={autopf}\{#AppName}

; Both directives, as Examples\64Bit.iss sets them for exactly this case: one
; payload, built x64. Allowed keeps Setup off machines where that binary cannot
; run at all — 32-bit Windows, and Arm64 Windows 10, which has no x64 emulation
; — instead of installing something that will not start. InstallIn64BitMode is
; the one that matters here: in the default 32-bit install mode every
; HKLM\Software\Classes write below is redirected into Wow6432Node where 64-bit
; Explorer never sees the associations, and {autopf} resolves to
; "Program Files (x86)". Change both if the packaged binary is ever Arm64.
; x64compatible is an Inno Setup 6.3 identifier; make-installer.ps1 refuses
; anything older.
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

; The HKCU PATH entry below is a "user area", which the compiler warns about in
; an administrative-mode script because it usually is a mistake. This one is
; not: it is guarded by a Check that fires only in the mode where HKCU is the
; right hive. A compile-time warning cannot see a Check, so it is turned off.
UsedUserAreasWarning=no
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes

; The running server holds wudict.exe open, so an upgrade cannot replace it
; while it runs. Restart Manager is told to close it: the alternative is a
; "file in use" dialog in the middle of an install the user just asked for.
; Safe to force — SQLite's journal makes an abrupt exit recoverable by design —
; and not restarted afterwards, because the user may not have wanted it running.
CloseApplications=force
RestartApplications=no

; The PATH entries below write to the machine or the user environment; this
; broadcasts the change so already-open Explorer-launched shells pick it up.
ChangesEnvironment=yes

; Its twin for the [Registry] file types: Setup tells Explorer to reload its
; association data at the end of the install, and Uninstall does the same at the
; end of the uninstall. Without it the new file icons do not appear until the
; user signs out. This is the directive Examples\Example3.iss uses next to its
; OpenWithProgids entries, and it replaces a hand-imported SHChangeNotify that
; used to live in [Code] doing the same two calls by hand.
ChangesAssociations=yes

SetupIconFile=wudict.ico
UninstallDisplayIcon={app}\wudict.exe,0
; Not "modern dynamic", which is what every example in issrc now uses: the
; dynamic appearance mode follows the system light/dark setting but arrived in
; Inno Setup 6.6 (2025-11-11). The floor is 6.3 because correctness needs it;
; raising it another three releases for the wizard's colours is not a trade
; worth making. Add the word once 6.6 is unremarkable.
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes
OutputDir={#OutputDir}
OutputBaseFilename=wudict-windows-x64-setup-{#NumVersion}
LicenseFile=..\..\LICENSE

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon";  Description: "Create a &desktop shortcut"; Flags: unchecked
Name: "startup";      Description: "Start {#AppName} at sign-in"; Flags: unchecked
Name: "addtopath";    Description: "Add {#AppName} to my &PATH (so `wudict` works in a terminal)"
Name: "associate";    Description: "Offer {#AppName} in ""Open with"" for dictionary files"

[Files]
Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "wudict.exe"; Flags: ignoreversion
Source: "wudict.ico";   DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#AppName}";       Filename: "{app}\wudict.exe"; IconFilename: "{app}\wudict.ico"; Comment: "Serve your dictionaries in the browser"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\wudict.exe"; IconFilename: "{app}\wudict.ico"; Tasks: desktopicon
; A Startup-folder shortcut rather than a Run registry key: it is the one place
; a user can find and delete an autostart entry without a tool. {autostartup},
; so an all-users install starts it for whoever signs in and a per-user install
; only for that one user. Never {userstartup}: in an elevated install that is
; the profile of whoever's administrator credentials the UAC prompt collected,
; which may not be the person installing. --no-browser because a tab that opens
; itself at every sign-in is a nuisance; the tray icon is the liveness
; indicator there (D74).
Name: "{autostartup}\{#AppName}"; Filename: "{app}\wudict.exe"; Parameters: "--no-browser"; IconFilename: "{app}\wudict.ico"; Tasks: startup

[Registry]
; PATH. Which variable this is depends on the install mode, and the two do not
; share a key, so HKA cannot express it — two entries with complementary Checks
; do, the way Examples\Example3.iss splits its HKLM and HKCU settings. The
; machine one lives outside HKLM\Software and is therefore never
; WOW64-redirected. preservestringtype keeps an existing REG_SZ from being
; widened, and uninsdeletevalue is deliberately NOT used: it would delete the
; whole Path. Removal is surgical, in CurUninstallStepChanged below.
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
    ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
    Flags: preservestringtype; Tasks: addtopath; \
    Check: IsAdminInstallMode and NeedsAddPath('{app}')
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; \
    ValueData: "{olddata};{app}"; Flags: preservestringtype; Tasks: addtopath; \
    Check: (not IsAdminInstallMode) and NeedsAddPath('{app}')

; File types, under HKA — HKLM for an all-users install so every account sees
; the entry, HKCU otherwise. Explorer merges the two into HKCR either way.
; This registers a ProgId and adds it to each extension's OpenWithProgids list
; — it does NOT seize the default handler. That is not politeness: since
; Windows 10 the default association can only be changed by the user in
; Settings, and an installer that writes the key anyway gets its choice
; reverted and the user shown a "an app caused a problem" notice. Appearing in
; "Open with" is the whole of what an installer is allowed to do.
Root: HKA; Subkey: "Software\Classes\{#ProgId}"; ValueType: string; ValueName: ""; \
    ValueData: "Dictionary"; Flags: uninsdeletekey; Tasks: associate
Root: HKA; Subkey: "Software\Classes\{#ProgId}\DefaultIcon"; ValueType: string; ValueName: ""; \
    ValueData: "{app}\wudict.ico,0"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\{#ProgId}\shell\open\command"; ValueType: string; ValueName: ""; \
    ValueData: """{app}\wudict.exe"" ""%1"""; Tasks: associate

Root: HKA; Subkey: "Software\Classes\.mdx\OpenWithProgids";  ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate
Root: HKA; Subkey: "Software\Classes\.dsl\OpenWithProgids";  ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate
Root: HKA; Subkey: "Software\Classes\.slob\OpenWithProgids"; ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate
Root: HKA; Subkey: "Software\Classes\.bgl\OpenWithProgids";  ValueType: string; ValueName: "{#ProgId}"; ValueData: ""; Flags: uninsdeletevalue; Tasks: associate

[Run]
; postinstall implies runasoriginaluser, which is load-bearing once this can be
; an elevated install: the server must come up as the person who installed it,
; not as the administrator whose credentials the UAC prompt took, or it would
; read that account's dictionaries and write that account's config.
Filename: "{app}\wudict.exe"; Description: "Start {#AppName} now"; Flags: nowait postinstall runasoriginaluser skipifsilent

[Code]
const
  // The two PATH variables. The machine one is deliberately not under
  // HKLM\Software, so no WOW64 view question arises for it.
  MachinePathKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';
  UserPathKey    = 'Environment';

// Position of Dir within the semicolon-separated PATH held at Root\Key, or 0
// when it is not there — including when there is no Path value at all, which
// is the same answer for both callers. Searched in a copy wrapped in sentinels
// so "C:\wu" never matches inside "C:\wudict"; that wrapper is one character
// longer at the front, so a hit at P in it is the first character of Dir at P
// in Existing.
function PathIndex(Root: Integer; const Key, Dir: string; var Existing: string): Integer;
begin
  Result := 0;
  if not RegQueryStringValue(Root, Key, 'Path', Existing) then
    Exit;
  Result := Pos(';' + Uppercase(Dir) + ';', ';' + Uppercase(Existing) + ';');
end;

// The Check behind both [Registry] PATH entries: the install mode picks which
// variable is asked about, and each entry's own Check decides which is written.
function NeedsAddPath(Param: string): Boolean;
var
  Existing: string;
begin
  if IsAdminInstallMode then
    Result := PathIndex(HKEY_LOCAL_MACHINE, MachinePathKey, ExpandConstant(Param), Existing) = 0
  else
    Result := PathIndex(HKEY_CURRENT_USER, UserPathKey, ExpandConstant(Param), Existing) = 0;
end;

// Uninstall only. uninsdeletevalue is not usable on Path — it would delete the
// whole variable — so removal is surgical.
procedure RemoveFromPath(Root: Integer; const Key, Dir: string);
var
  Existing: string;
  P: Integer;
begin
  P := PathIndex(Root, Key, Dir, Existing);
  if P = 0 then
    Exit;
  Delete(Existing, P, Length(Dir) + 1);
  while (Length(Existing) > 0) and (Existing[Length(Existing)] = ';') do
    Delete(Existing, Length(Existing), 1);
  while (Length(Existing) > 0) and (Existing[1] = ';') do
    Delete(Existing, 1, 1);
  if Length(Existing) = 0 then
    RegDeleteValue(Root, Key, 'Path')
  else
    RegWriteExpandStringValue(Root, Key, 'Path', Existing);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Dir: string;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    // Both hives, without asking which mode this was. {app} names this one
    // installation, so removing it wherever it appears is correct, idempotent,
    // and also tidies up after someone who installed one way and then the
    // other. An unelevated uninstaller simply fails the machine write, which
    // is precisely the case where there is nothing there to remove.
    Dir := ExpandConstant('{app}');
    RemoveFromPath(HKEY_LOCAL_MACHINE, MachinePathKey, Dir);
    RemoveFromPath(HKEY_CURRENT_USER, UserPathKey, Dir);
  end;
end;
