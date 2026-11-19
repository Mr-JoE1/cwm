# cwm installer for Windows (SPEC-V0.6 G, D37; look: SPEC-V0.6.1, D51). Windows PowerShell 5.1+.
#
#   irm https://mr-joe1.github.io/cwm/install.ps1 | iex
#
# With options (irm | iex cannot pass parameters; use env vars or a scriptblock):
#   $env:CWM_NO_SERVICE = '1'; irm https://mr-joe1.github.io/cwm/install.ps1 | iex
#   & ([scriptblock]::Create((irm https://mr-joe1.github.io/cwm/install.ps1))) -Version v0.6.1 -NoService
#
# Downloads the release zip for this CPU, verifies its SHA-256 against the
# release's checksums.txt (aborts on any mismatch), installs cwm.exe and
# cwmd.exe per user into %LOCALAPPDATA%\cwm\bin (no admin), adds that
# folder to the user Path, runs `cwm init` when needed, installs the logon
# autostart and opens the dashboard. Files fetched with Invoke-WebRequest
# carry no Mark-of-the-Web, so SmartScreen does not prompt.
#
# Parameters / env:  -Version X   / CWM_VERSION     release to install (default latest)
#                    -NoService   / CWM_NO_SERVICE=1  skip `cwm service install`
#                    -NoOpen      / CWM_NO_OPEN=1     do not open the dashboard
#                    -Uninstall   / CWM_UNINSTALL=1   remove cwm (keeps ~\.cwm)
# Look:              NO_COLOR (no colours); CWM_INSTALL_FANCY=1 forces the
#                    terminal layout when output is redirected (test seam)
# Test seams (env):  CWM_INSTALL_API, CWM_INSTALL_BASE_URL, CWM_INSTALL_DIR
#
# This file is pure ASCII on purpose: `irm | iex` decodes it with whatever
# charset the web server claims, so every non-ASCII glyph is built from its
# code point at run time.

[CmdletBinding()]
param(
    [string]$Version = $env:CWM_VERSION,
    [switch]$NoService,
    [switch]$NoOpen,
    [switch]$Uninstall
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is very slow with the progress bar in 5.1

$Repo = 'Mr-JoE1/cwm'
$Pages = 'https://mr-joe1.github.io/cwm'

# --- presentation -------------------------------------------------------------
# Fancy layout + colours on a console; plain ASCII when redirected. Unicode
# glyphs only where the font is known to have them (Windows Terminal, VS
# Code, non-Windows hosts); the legacy console gets glyphs that exist in
# Consolas and code page 437.

function Initialize-Ui {
    $redirected = $true
    try { $redirected = [Console]::IsOutputRedirected } catch { $redirected = $true }
    $script:Fancy = (-not $redirected) -or ($env:CWM_INSTALL_FANCY -eq '1')
    $script:Color = $script:Fancy -and (-not $env:NO_COLOR)
    $onWindows = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    $modern = [bool]$env:WT_SESSION -or ($env:TERM_PROGRAM -eq 'vscode') -or (-not $onWindows)
    $script:Unicode = $script:Fancy -and $modern
    if ($script:Unicode) {
        $script:G = @{ Ok = [string][char]0x2713; Warn = [string][char]0x26A0; Fail = [string][char]0x2717
            Bar = [string][char]0x2502; Dots = [string][char]0x2026
            TL = [string][char]0x256D; TR = [string][char]0x256E; BL = [string][char]0x2570; BR = [string][char]0x256F
            H = [string][char]0x2500; V = [string][char]0x2502 }
    } elseif ($script:Fancy) {
        # Legacy console: square root as the check mark, single-line box
        # drawing - all present in Consolas, Lucida Console and CP437.
        $script:G = @{ Ok = [string][char]0x221A; Warn = '!'; Fail = 'x'
            Bar = [string][char]0x2502; Dots = '...'
            TL = [string][char]0x250C; TR = [string][char]0x2510; BL = [string][char]0x2514; BR = [string][char]0x2518
            H = [string][char]0x2500; V = [string][char]0x2502 }
    } else {
        $script:G = @{ Ok = 'ok'; Warn = '!!'; Fail = 'xx'; Bar = '|'; Dots = '...'
            TL = '+'; TR = '+'; BL = '+'; BR = '+'; H = '-'; V = '|' }
    }
    $script:StepN = 0
    $script:Steps = 8
    $script:OpenLine = $false
}

# Use-Utf8Console: cwm.exe writes UTF-8 and PowerShell decodes native
# output with [Console]::OutputEncoding - the OEM code page by default,
# which turned the v0.6.0 check mark into mojibake. Switched for this
# session only; Restore-Console puts the old encoding back. The child cwm
# processes are told which glyph set to use (CWM_GLYPHS, D50) so their
# lines match the installer's.
function Use-Utf8Console {
    $script:PrevOutEnc = $null
    $script:PrevGlyphs = $env:CWM_GLYPHS
    try {
        $script:PrevOutEnc = [Console]::OutputEncoding
        [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
    } catch { $script:PrevOutEnc = $null }
    if ($script:Unicode) { $env:CWM_GLYPHS = 'unicode' } else { $env:CWM_GLYPHS = 'ascii' }
}

function Restore-Console {
    if ($null -ne $script:PrevOutEnc) {
        try { [Console]::OutputEncoding = $script:PrevOutEnc } catch { }
    }
    $env:CWM_GLYPHS = $script:PrevGlyphs
}

function Write-Part([string]$text, [string]$color) {
    if ($script:Color -and $color) {
        Write-Host $text -NoNewline -ForegroundColor $color
    } else {
        Write-Host $text -NoNewline
    }
}

function Close-Line {
    if ($script:OpenLine) { Write-Host ''; $script:OpenLine = $false }
}

function Write-Banner([string]$sub) {
    Write-Host ''
    Write-Part '                ___ __      __ _ __ ___' 'White'; Write-Host ''
    Write-Part '   ______     ' 'Cyan'; Write-Part ' / __|\ \ /\ / /| ''_ ` _ \' 'White'; Write-Host ''
    Write-Part '  /  ' 'Cyan'; Write-Part '>_' 'White'; Write-Part '  \    ' 'Cyan'; Write-Part '| (__  \ V  V / | | | | | |' 'White'; Write-Host ''
    Write-Part '  \______/    ' 'Cyan'; Write-Part ' \___|  \_/\_/  |_| |_| |_|' 'White'; Write-Host ''
    Write-Part '  Claude Workspace Manager  ' 'White'; Write-Part $sub 'DarkGray'; Write-Host ''
    Write-Host ''
}

function Start-Step([string]$title) {
    Close-Line
    $script:StepN++
    Write-Part ('[{0}/{1}]' -f $script:StepN, $script:Steps) 'Cyan'
    Write-Part " $title " ''
    Write-Part $script:G.Dots 'DarkGray'
    Write-Part ' ' ''
    $script:OpenLine = $true
}

function Write-Result([string]$glyph, [string]$color, [string]$text, [string]$detail) {
    if (-not $script:OpenLine) { Write-Part '      ' '' }
    Write-Part $glyph $color
    Write-Part " $text" ''
    if ($detail) { Write-Part " $detail" 'DarkGray' }
    Write-Host ''
    $script:OpenLine = $false
}
function Step-Ok([string]$text, [string]$detail) { Write-Result $script:G.Ok 'Green' $text $detail }
function Step-Warn([string]$text, [string]$detail) { Write-Result $script:G.Warn 'Yellow' $text $detail }
function Step-Skip([string]$text, [string]$detail) { Write-Result '-' 'DarkGray' $text $detail }

function Write-Note([string]$text) {
    Close-Line
    Write-Part "      $text" 'DarkGray'
    Write-Host ''
}

# Fail aborts the install; the top level prints it (see the bottom).
function Fail([string]$msg) { throw "cwm-install: error: $msg" }

function Write-Failure([string]$msg) {
    Close-Line
    $m = $msg -replace '^cwm-install: error: ', ''
    Write-Part "$($script:G.Fail) error:" 'Red'
    Write-Host " $m"
}

# Write-Summary: the closing box; its width follows the longest line.
function Write-Summary([string[]]$lines) {
    $w = 0
    foreach ($l in $lines) { if ($l.Length -gt $w) { $w = $l.Length } }
    $bar = $script:G.H * ($w + 2)
    Write-Host ''
    Write-Part ('  ' + $script:G.TL + $bar + $script:G.TR) 'Cyan'; Write-Host ''
    $first = $true
    foreach ($l in $lines) {
        Write-Part ('  ' + $script:G.V) 'Cyan'
        if ($first) { Write-Part (' ' + $l.PadRight($w) + ' ') 'White'; $first = $false } else { Write-Part (' ' + $l.PadRight($w) + ' ') '' }
        Write-Part $script:G.V 'Cyan'
        Write-Host ''
    }
    Write-Part ('  ' + $script:G.BL + $bar + $script:G.BR) 'Cyan'; Write-Host ''
    Write-Host ''
}

function Format-Size([long]$n) {
    if ($n -ge 1MB) { return ('{0:N1} MB' -f ($n / 1MB)) }
    if ($n -ge 1KB) { return ('{0:N0} KB' -f ($n / 1KB)) }
    return "$n B"
}

# --- mechanics ----------------------------------------------------------------

function Get-Arch {
    $a = $env:PROCESSOR_ARCHITEW6432
    if (-not $a) { $a = $env:PROCESSOR_ARCHITECTURE }
    switch ($a) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { Fail "unsupported CPU '$a' (cwm ships amd64 and arm64)" }
    }
}

function Get-BinDir {
    if ($env:CWM_INSTALL_DIR) { return $env:CWM_INSTALL_DIR }
    return (Join-Path $env:LOCALAPPDATA 'cwm\bin')
}

function Get-CwmHome {
    if ($env:CWM_HOME) { return $env:CWM_HOME }
    return (Join-Path $HOME '.cwm')
}

function Get-LatestTag {
    $api = 'https://api.github.com'
    if ($env:CWM_INSTALL_API) { $api = $env:CWM_INSTALL_API }
    try {
        $rel = Invoke-RestMethod -UseBasicParsing -Uri "$api/repos/$Repo/releases/latest" -Headers @{ 'User-Agent' = 'cwm-installer' }
        if ($rel.tag_name) { return [string]$rel.tag_name }
    } catch {
        Fail "could not determine the latest release ($($_.Exception.Message)); pass -Version vX.Y.Z"
    }
    Fail 'the release API returned no tag_name; pass -Version vX.Y.Z'
}

function Get-File([string]$url, [string]$dest) {
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $dest -Headers @{ 'User-Agent' = 'cwm-installer' }
    } catch {
        Fail "download failed: $url ($($_.Exception.Message))"
    }
}

function Test-InUserPath([string]$dir) {
    $p = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $p) { return $false }
    foreach ($e in $p.Split(';')) {
        if ($e.TrimEnd('\') -ieq $dir.TrimEnd('\')) { return $true }
    }
    return $false
}

# Add-UserPath returns $true when it changed the user Path.
function Add-UserPath([string]$dir) {
    $changed = $false
    if (-not (Test-InUserPath $dir)) {
        $p = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ($p) { $p = $p.TrimEnd(';') + ';' + $dir } else { $p = $dir }
        [Environment]::SetEnvironmentVariable('Path', $p, 'User')
        $changed = $true
    }
    $cur = [string]$env:Path
    if (-not (($cur.Split(';')) -contains $dir)) { $env:Path = (@($dir, $cur) | Where-Object { $_ }) -join ';' }
    return $changed
}

# Remove-UserPath returns $true when it changed the user Path.
function Remove-UserPath([string]$dir) {
    $p = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $p) { return $false }
    $kept = @($p.Split(';') | Where-Object { $_ -and ($_.TrimEnd('\') -ine $dir.TrimEnd('\')) })
    $new = $kept -join ';'
    if ($new -ne $p) {
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
        return $true
    }
    return $false
}

# Invoke-CwmStep runs cwm with its output (stdout + stderr) indented and
# dimmed under the current step, keeps the lines in $script:ChildLog and
# returns only the exit code. 5.1 turns native stderr lines into error
# records under 2>&1; with 'Stop' the first one would abort the install.
function Invoke-CwmStep([string]$exe, [string[]]$cwmArgs) {
    Close-Line
    $script:ChildLog = New-Object System.Collections.Generic.List[string]
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $exe @cwmArgs 2>&1 | ForEach-Object {
            $line = [string]$_
            $script:ChildLog.Add($line)
            Write-Part ('      ' + $script:G.Bar + ' ' + $line) 'DarkGray'
            Write-Host ''
        }
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prev
    }
    return $code
}

# Invoke-CwmQuiet runs cwm discarding its output (best-effort calls).
function Invoke-CwmQuiet([string]$exe, [string[]]$cwmArgs) {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & $exe @cwmArgs 2>&1 | Out-Null } finally { $ErrorActionPreference = $prev }
}

function Uninstall-Cwm {
    $script:Steps = 3
    Write-Banner 'uninstaller'
    $bin = Get-BinDir
    $exe = Join-Path $bin 'cwm.exe'

    Start-Step 'Removing the logon autostart'
    if (Test-Path -LiteralPath $exe) {
        if ((Invoke-CwmStep $exe @('service', 'uninstall')) -eq 0) { Step-Ok 'autostart removed' '' } else { Step-Warn 'service uninstall reported an error (continuing)' '' }
        Invoke-CwmQuiet $exe @('service', 'stop')
    } else {
        Step-Skip "no cwm at $exe" ''
    }

    Start-Step 'Removing the binaries'
    $removed = 0
    foreach ($f in @('cwm.exe', 'cwmd.exe', 'cwm.exe.old', 'cwmd.exe.old')) {
        $p = Join-Path $bin $f
        if (Test-Path -LiteralPath $p) { Remove-Item -LiteralPath $p -Force; $removed++ }
    }
    if ($removed -gt 0) { Step-Ok "removed cwm from $bin" '' } else { Step-Skip 'nothing to remove' '' }

    Start-Step 'Cleaning the user Path'
    if (Remove-UserPath $bin) { Step-Ok "removed $bin from your user Path" '' } else { Step-Skip 'not on the user Path' '' }

    Write-Summary @('cwm was uninstalled', '', "Your workspace ($(Get-CwmHome)) was kept -", 'delete it yourself if you want it gone.')
}

function Install-Cwm([string]$tag, [bool]$skipService, [bool]$skipOpen) {
    # Windows PowerShell 5.1 may still default to TLS 1.0; GitHub needs 1.2.
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch { }

    $arch = Get-Arch
    $which = 'pinned'
    if (-not $tag) { $tag = Get-LatestTag; $which = 'latest' }
    if (-not $tag.StartsWith('v')) { $tag = "v$tag" }
    if ($tag -notmatch '^v[A-Za-z0-9._+-]+$') { Fail "invalid version '$tag'" }
    $num = $tag.Substring(1)
    $archive = "cwm_${num}_windows_${arch}.zip"
    $base = "https://github.com/$Repo/releases/download"
    if ($env:CWM_INSTALL_BASE_URL) { $base = $env:CWM_INSTALL_BASE_URL }
    $base = "$base/$tag"
    $bin = Get-BinDir

    Write-Banner ("installer $($script:G.Dots) cwm $tag")
    Start-Step 'Detecting platform'
    Step-Ok "windows/$arch" "release $tag ($which)"

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("cwm-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Start-Step "Downloading $archive"
        $zip = Join-Path $tmp $archive
        Get-File "$base/$archive" $zip
        Get-File "$base/checksums.txt" (Join-Path $tmp 'checksums.txt')
        Step-Ok (Format-Size (Get-Item -LiteralPath $zip).Length) ''

        Start-Step 'Verifying SHA-256'
        $want = $null
        foreach ($line in Get-Content -LiteralPath (Join-Path $tmp 'checksums.txt')) {
            $parts = $line.Trim() -split '\s+'
            if ($parts.Count -ge 2 -and ($parts[1].TrimStart('*') -eq $archive)) { $want = $parts[0].ToLowerInvariant(); break }
        }
        if (-not $want) { Fail "$archive is not listed in checksums.txt - refusing to install" }
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
        if ($got -ne $want) { Fail "checksum mismatch for $archive (expected $want, got $got) - nothing was installed" }
        Step-Ok 'checksum OK' $got.Substring(0, 12)

        Start-Step 'Installing'
        $x = Join-Path $tmp 'x'
        Expand-Archive -LiteralPath $zip -DestinationPath $x -Force
        if (-not (Test-Path -LiteralPath (Join-Path $x 'cwm.exe'))) { Fail 'archive has no cwm.exe' }

        New-Item -ItemType Directory -Force -Path $bin | Out-Null
        $exe = Join-Path $bin 'cwm.exe'
        $upgraded = ''
        if (Test-Path -LiteralPath $exe) {
            # Upgrade: stop the running agent, then move the old binaries
            # aside (a running .exe can be renamed, not overwritten).
            Invoke-CwmQuiet $exe @('service', 'stop')
            $upgraded = ' (upgraded)'
        }
        foreach ($f in @('cwm.exe', 'cwmd.exe')) {
            $src = Join-Path $x $f
            if (-not (Test-Path -LiteralPath $src)) { continue }
            $dst = Join-Path $bin $f
            $old = "$dst.old"
            if (Test-Path -LiteralPath $old) { Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue }
            if (Test-Path -LiteralPath $dst) { Move-Item -LiteralPath $dst -Destination $old -Force }
            Copy-Item -LiteralPath $src -Destination $dst -Force
        }
        $ver = [string](& $exe --version)
        Step-Ok ("$exe$upgraded") $ver
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    Start-Step 'Updating the user Path'
    if (Add-UserPath $bin) {
        Step-Ok "added $bin to your user Path" '(new terminals pick it up)'
    } else {
        Step-Skip 'already on your user Path' ''
    }

    Start-Step 'Initializing workspace'
    if (Test-Path -LiteralPath (Join-Path (Get-CwmHome) 'config.toml')) {
        Step-Skip 'already initialized' (Get-CwmHome)
    } elseif ((Invoke-CwmStep $exe @('init')) -eq 0) {
        Step-Ok 'workspace ready' ''
    } else {
        Step-Warn 'cwm init failed - run it yourself later' ''
    }

    $urlLines = @()
    Start-Step 'Installing logon autostart'
    if ($skipService) {
        Step-Skip 'skipped (-NoService)' ''
    } elseif ((Invoke-CwmStep $exe @('service', 'install')) -eq 0) {
        $urlLines += $script:ChildLog
        Step-Ok 'the agent starts at every logon' ''
    } else {
        Step-Warn "autostart could not be installed - run 'cwm service install' later" ''
    }

    Start-Step 'Opening the dashboard'
    if ($skipOpen) {
        Step-Skip 'skipped (-NoOpen)' ''
    } elseif ((Invoke-CwmStep $exe @('dashboard')) -eq 0) {
        $urlLines = @($script:ChildLog) + $urlLines
        Step-Ok 'dashboard opened in your browser' ''
    } else {
        Step-Warn "could not open the dashboard - run 'cwm dashboard'" ''
    }

    # The URL printed by `cwm dashboard` / `cwm service install` (those
    # lines never carry the token).
    $url = 'run: cwm dashboard'
    foreach ($l in $urlLines) {
        if ($l -match '(http://[^ /?]+/)') { $url = $Matches[1]; break }
    }

    Write-Summary @(
        "cwm $tag is installed",
        '',
        "  binary      $exe",
        "  version     $num (windows/$arch)",
        "  dashboard   $url",
        '',
        'Next steps',
        '  1. open the dashboard    cwm dashboard',
        '  2. add a Claude login    cwm account add main',
        '     (or use the setup wizard in the dashboard)',
        '',
        'Uninstall',
        ('$env:CWM_UNINSTALL=''1''; irm ' + $Pages + '/install.ps1 | iex')
    )
}

Initialize-Ui
Use-Utf8Console
try {
    if ($Uninstall -or $env:CWM_UNINSTALL -eq '1') {
        Uninstall-Cwm
    } else {
        $skipService = $NoService.IsPresent -or ($env:CWM_NO_SERVICE -eq '1')
        $skipOpen = $NoOpen.IsPresent -or ($env:CWM_NO_OPEN -eq '1')
        Install-Cwm $Version $skipService $skipOpen
    }
} catch {
    Write-Failure ([string]$_.Exception.Message)
    Restore-Console
    # Run as a file (-File, or & .\install.ps1): a real exit code. Through
    # `irm | iex` an exit would close the user's PowerShell window, so the
    # failure is reported and $LASTEXITCODE set instead.
    if ($PSCommandPath) { exit 1 }
    $global:LASTEXITCODE = 1
    return
}
Restore-Console
