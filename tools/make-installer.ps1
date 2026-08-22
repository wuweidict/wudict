# Copyright (C) 2026 glowinthedark
#
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Compile the Windows installer (P86/D76) with Inno Setup 6.
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
#   .\tools\make-installer.ps1 -Exe .\wudict.exe -OutDir .\dist
[CmdletBinding()]
param(
    # The wudict.exe to package.
    [string] $Exe,
    # Where the setup .exe is written.
    [string] $OutDir,
    # ISCC.exe, if it is somewhere non-standard.
    [string] $Iscc
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
if (-not $Exe)    { $Exe    = Join-Path $root 'wudict.exe' }
if (-not $OutDir) { $OutDir = Join-Path $root 'dist' }
$iss = Join-Path $root 'packaging\windows\wudict.iss'
$ico = Join-Path $root 'packaging\windows\wudict.ico'

if (-not (Test-Path -LiteralPath $Exe -PathType Leaf)) {
    throw "make-installer: no binary at $Exe — build the windows/amd64 exe first"
}
if (-not (Test-Path -LiteralPath $iss -PathType Leaf)) { throw "make-installer: missing $iss" }
if (-not (Test-Path -LiteralPath $ico -PathType Leaf)) { throw "make-installer: missing $ico — run: make icons" }

# ISCC is not on PATH even when Inno Setup is installed. Written as plain
# if-blocks rather than a filtered pipeline: ${env:ProgramFiles(x86)} is
# undefined on some hosts, and Join-Path throws on a null root.
if (-not $Iscc) {
    $found = Get-Command 'iscc.exe' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($found) { $Iscc = $found.Source }
}
if (-not $Iscc) {
    foreach ($dir in @(${env:ProgramFiles(x86)}, $env:ProgramFiles)) {
        if (-not $dir) { continue }
        $candidate = Join-Path $dir 'Inno Setup 6\ISCC.exe'
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { $Iscc = $candidate; break }
    }
}
if (-not $Iscc) {
    throw @'
make-installer: Inno Setup 6 not found.
  winget install JRSoftware.InnoSetup   (or pass -Iscc C:\path\to\ISCC.exe)
  Off Windows there is nothing to run: CI builds the installer in the
  windows-installer job of .github\workflows\build-cgo.yml
'@
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
$num = '0.0.0'
if ($version -match '^[vV]?(?<n>\d+(?:\.\d+)*)') {
    $parts = $Matches['n'].Split('.')
    $num = (0..2 | ForEach-Object { if ($_ -lt $parts.Count) { $parts[$_] } else { '0' } }) -join '.'
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

# Splatted rather than backtick-continued: a backtick with trailing whitespace
# after it silently ends the statement, and this is the one call that matters.
# PowerShell 7 quotes native arguments correctly, which is why the Makefile
# defaults to `pwsh`; Windows PowerShell 5.1 does not always, so a repository
# path containing spaces wants pwsh rather than powershell.exe.
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

$setup = Join-Path $OutDir "wudict-setup-$num.exe"
if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw "make-installer: iscc reported success but $setup is missing"
}

Write-Host "built $setup"
Write-Host "  name    $name $version (version $num)"
Write-Host "  install per-user, no admin prompt: %LOCALAPPDATA%\Programs\$name"
