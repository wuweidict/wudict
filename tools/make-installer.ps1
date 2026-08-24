# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Compile the Windows installer (P86/D76) with Inno Setup 6.
#
# NOTE: This file is UTF-8 WITH a BOM, and must stay that way. Windows PowerShell
# 5.1 — the powershell.exe on every Windows, and what `make win-installer`
# runs — decodes a BOM-less .ps1 as the ANSI code page, where a UTF-8 em dash
# arrives as two Windows-1252 characters, one of which is a curly quote that
# the parser accepts as a string delimiter. The script then fails to parse for
# reasons that point at the wrong line. pwsh 7 defaults to UTF-8 and never
# sees it, which is exactly why CI would not have caught this.
#
# PowerShell, not sh, and the rule is general: each packager is written in the
# shell native to the only OS that can run its tool. tools/make-app.sh is sh
# because codesign/plutil/iconutil are macOS-only; this is PowerShell because
# ISCC.exe is Windows-only. Reaching for git-bash here buys nothing and costs
# the MSYS argument rewriter, which turns "/DAppName=wuDict" into
# "C:\Program Files\Git\DAppName=wuDict" before ISCC ever sees it.
#
# Everything that decides what the installer DOES lives in
# packaging\windows\wudict.iss. This only locates the compiler and derives the
# product identity from the binary.
#
# Run it with no arguments from the repository root, which is what
# `make win-installer` and the CI job both do:
#
#   .\tools\make-installer.ps1
#
# Every path it needs is derived from $PSScriptRoot, so it is always a native
# Windows path — the one thing a caller cannot be relied on to supply. -Exe and
# -OutDir override the defaults (repo root \ wudict.exe, repo root \ dist).
[CmdletBinding()]
param(
    # The wudict.exe to package.
    [string] $Exe,
    # Where the setup .exe is written.
    [string] $OutDir,
    # ISCC.exe, if it is somewhere non-standard.
    [string] $Iscc,
    # Print the Inno Setup compiler this script would use and stop. A query,
    # so "nothing found" prints nothing and still exits 0 — that lets a caller
    # (CI, or you) decide what to do about it.
    [switch] $Locate
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
if (-not $Exe)    { $Exe    = Join-Path $root 'wudict.exe' }
if (-not $OutDir) { $OutDir = Join-Path $root 'dist' }
$iss = Join-Path $root 'packaging\windows\wudict.iss'
$ico = Join-Path $root 'packaging\windows\wudict.ico'

# Version strings, padded to exactly three fields: "v1.2" -> "1.2.0". Used for
# the installer's numeric VersionInfoVersion and to rank the Inno Setup
# installs found below, so the padding rule is stated once.
function Get-PaddedVersion {
    param([string] $Text)
    $m = [regex]::Match([string]$Text, '^[vV]?(\d+(?:\.\d+)*)')
    if (-not $m.Success) { return '0.0.0' }
    $parts = $m.Groups[1].Value.Split('.')
    return (0..2 | ForEach-Object { if ($_ -lt $parts.Count) { $parts[$_] } else { '0' } }) -join '.'
}

# Inno Setup is never on PATH, and its directory is not fixed: the installer
# offers to change it, and winget/scoop/portable copies all land somewhere
# else. Its own uninstall entry always knows, so ask that rather than guess at
# "%ProgramFiles(x86)%\Inno Setup 6". Three hives because a per-machine install
# writes the 32-bit view (Inno is a 32-bit program, hence WOW6432Node), a
# 32-bit-only host writes the plain view, and a per-user install writes HKCU.
#
# Properties are reached through PSObject.Properties rather than directly:
# Set-StrictMode makes a missing property a terminating error, and plenty of
# uninstall entries have no InstallLocation at all.
function Find-Iscc {
    $keys = @(
        'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\Inno Setup *'
        'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Inno Setup *'
        'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Inno Setup *'
    )
    $found = @()
    foreach ($entry in @(Get-ItemProperty -Path $keys -ErrorAction SilentlyContinue)) {
        $props = $entry.PSObject.Properties
        $dir = if ($props['InstallLocation']) { [string]$props['InstallLocation'].Value } else { '' }
        if (-not $dir) { continue }
        $exe = Join-Path $dir 'ISCC.exe'
        if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { continue }
        $raw = if ($props['DisplayVersion']) { [string]$props['DisplayVersion'].Value } else { '0' }
        $found += [pscustomobject]@{ Path = $exe; Version = [version](Get-PaddedVersion $raw) }
    }
    if ($found.Count -gt 0) {
        return ($found | Sort-Object Version -Descending | Select-Object -First 1)
    }
    # A copy on PATH that no uninstall entry describes — a portable unzip, or a
    # CI image that dropped it there. Version unknown, so trust it.
    $onPath = Get-Command 'iscc.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($onPath) { return [pscustomobject]@{ Path = $onPath.Source; Version = [version]'0.0.0' } }
    return $null
}

if ($Locate) {
    if ($Iscc) { Write-Output $Iscc } else {
        $hit = Find-Iscc
        if ($hit) { Write-Output $hit.Path }
    }
    exit 0
}

if (-not (Test-Path -LiteralPath $Exe -PathType Leaf)) {
    throw @"
make-installer: no binary at $Exe
  Build it first, from the repository root:
    go build -tags sqlite_fts5 -ldflags "-s -w" -o wudict.exe .
  (make build is a POSIX recipe and does not run on Windows; it would also
  write an extension-less "wudict", which Windows cannot execute.)
"@
}
if (-not (Test-Path -LiteralPath $iss -PathType Leaf)) { throw "make-installer: missing $iss" }
if (-not (Test-Path -LiteralPath $ico -PathType Leaf)) { throw "make-installer: missing $ico — run: make icons" }

if (-not $Iscc) {
    $hit = Find-Iscc
    if (-not $hit) {
        throw @'
make-installer: Inno Setup 6 not found.
  winget install JRSoftware.InnoSetup   (or pass -Iscc C:\path\to\ISCC.exe)
  6.3 or newer.
  Off Windows there is nothing to run: CI builds the installer in the
  windows-installer job of .github\workflows\build-cgo.yml
'@
    }
    # wudict.iss uses PrivilegesRequiredOverridesAllowed and the x64compatible
    # architecture identifier, which is 6.3 and later. An older compiler fails
    # on them with an error about a line number, which is a worse thing to be
    # told than this. Version 0 means "found on PATH, unknown" — let it try.
    if ($hit.Version.Major -ne 0 -and $hit.Version -lt [version]'6.3.0') {
        throw "make-installer: $($hit.Path) is Inno Setup $($hit.Version) — this installer needs 6.3 or newer"
    }
    $Iscc = $hit.Path
}

# Identity from the binary, so the name and version exist once — in
# internal/cli — rather than being copied into the build system. Same source as
# tools/version.sh feeds the macOS bundle; only the four lines that parse
# "<ProductName> <Version>" are stated twice, once per host shell.
$name = 'wuDict'
$version = 'dev'
try {
    $line = (& $Exe '--version' 2>$null | Select-Object -First 1)
    if ($line -match '^(?<n>[A-Za-z0-9._-]+)\s+(?<v>\S+)') {
        $name = $Matches['n']
        $version = $Matches['v']
    }
} catch {
    Write-Warning "make-installer: $Exe would not run — stamping the installer $name $version"
}

# CFBundleShortVersionString's Windows twin: VersionInfoVersion must be
# numeric, so take the tag part of `git describe` (v1.2.3-4-gdeadbee -> 1.2.3)
# and pad to exactly three fields.
$num = Get-PaddedVersion $version

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

# Splatted rather than backtick-continued: a backtick with trailing whitespace
# after it silently ends the statement, and this is the one call that matters.
# Runs under Windows PowerShell 5.1 as well as pwsh 7 — 5.1 quotes a native
# argument whenever it contains a space, which is all these need.
$isccArgs = @(
    "/DAppName=$name"
    "/DAppVersion=$version"
    "/DNumVersion=$num"
    "/DSourceExe=$((Resolve-Path -LiteralPath $Exe).Path)"
    "/DOutputDir=$((Resolve-Path -LiteralPath $OutDir).Path)"
    $iss
)
& $Iscc @isccArgs
if ($LASTEXITCODE -ne 0) { throw "make-installer: iscc exited $LASTEXITCODE" }

$setup = Join-Path $OutDir "wudict-windows-x64-setup-$num.exe"
if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw "make-installer: iscc reported success but $setup is missing"
}

Write-Host "built $setup"
Write-Host "  name    $name $version (version $num)"
Write-Host "  installs for all users by default (Program Files); the wizard's"
Write-Host "  first page offers per-user instead: %LOCALAPPDATA%\Programs\$name"
