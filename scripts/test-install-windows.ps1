# Windows installer harness (SPEC-V0.6.1): runs scripts/install.ps1 end to
# end under the PowerShell that runs this file - Windows PowerShell 5.1 in
# CI, the v0.6.0 tester's shell - against a snapshot release served from a
# local `python -m http.server`. CWM_SERVICE_DRYRUN=1 keeps the autostart
# step a printed plan; CWM_HOME / CWM_INSTALL_DIR point into a temp dir.
# The user Path IS edited (CI runners are throwaway; do not run this on
# your own machine unless you are fine with that).
#
# Also checks the v0.6.0 mojibake (D50): cwm.exe output piped through
# PowerShell with the OEM code page must be plain ASCII, never the (gamma, pound, o) of the report.
# And the banner (D59): under -File and under `irm | iex` (the tester's
# exact command, served by the local http.server), each banner line must be
# byte-identical to scripts/banner.txt - a dropped backtick fails the run.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\test-install-windows.ps1

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$sandbox = Join-Path ([IO.Path]::GetTempPath()) ('cwm-harness-' + [Guid]::NewGuid().ToString('N'))
$ver = '0.0.0-harness'
$tag = "v$ver"
$dl = Join-Path $sandbox "srv\download\$tag"
$api = Join-Path $sandbox 'srv\repos\Mr-JoE1\cwm\releases'
$build = Join-Path $sandbox 'build'
New-Item -ItemType Directory -Force -Path $dl, $api, $build | Out-Null

$script:pass = 0
$script:fail = 0
function Check([string]$name, [bool]$ok) {
    if ($ok) { $script:pass++; Write-Host "ok   $name" } else { $script:fail++; Write-Host "FAIL $name" }
}

# Test-Banner: the 4 art lines of scripts/banner.txt appear verbatim and in
# order in $text (case-sensitive, every character). Prints the first
# differing line so a CI log shows what got mangled.
$art = [IO.File]::ReadAllLines((Join-Path $repo 'scripts\banner.txt'))
function Test-Banner([string]$text) {
    $lines = @($text -split "`r?`n")
    $at = -1
    for ($i = 0; $i -lt $lines.Count; $i++) { if ($lines[$i] -ceq $art[0]) { $at = $i; break } }
    if ($at -lt 0) { Write-Host "    banner: first art line not found"; return $false }
    for ($k = 0; $k -lt $art.Count; $k++) {
        $got = ''
        if ($at + $k -lt $lines.Count) { $got = $lines[$at + $k] }
        if ($got -cne $art[$k]) { Write-Host "    banner line $($k + 1): got  [$got]"; Write-Host "    banner line $($k + 1): want [$($art[$k])]"; return $false }
    }
    return $true
}

$server = $null
try {
    Write-Host "== building snapshot $tag"
    $ld = "-s -w -X github.com/Mr-JoE1/cwm/internal/cli.Version=$ver -X github.com/Mr-JoE1/cwm/internal/cli.Commit=harness"
    Push-Location $repo
    try {
        & go build -trimpath -ldflags $ld -o (Join-Path $build 'cwm.exe') ./cmd/cwm
        if ($LASTEXITCODE -ne 0) { throw 'go build cwm failed' }
        & go build -trimpath -ldflags "$ld -H=windowsgui" -o (Join-Path $build 'cwmd.exe') ./cmd/cwmd
        if ($LASTEXITCODE -ne 0) { throw 'go build cwmd failed' }
    } finally { Pop-Location }
    $zipName = "cwm_${ver}_windows_amd64.zip"
    $zip = Join-Path $dl $zipName
    Compress-Archive -Path (Join-Path $build 'cwm.exe'), (Join-Path $build 'cwmd.exe') -DestinationPath $zip
    $sum = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $dl 'checksums.txt'), "$sum  $zipName`n")
    [IO.File]::WriteAllText((Join-Path $api 'latest'), "{`"tag_name`": `"$tag`"}")

    $port = Get-Random -Minimum 20000 -Maximum 40000
    $py = 'python'
    if (-not (Get-Command $py -ErrorAction SilentlyContinue)) { $py = 'python3' }
    $server = Start-Process -FilePath $py -ArgumentList @('-I', '-m', 'http.server', "$port", '--bind', '127.0.0.1', '--directory', (Join-Path $sandbox 'srv')) -PassThru -WindowStyle Hidden
    $up = $false
    for ($i = 0; $i -lt 50 -and -not $up; $i++) {
        try { Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$port/download/$tag/checksums.txt" | Out-Null; $up = $true } catch { Start-Sleep -Milliseconds 200 }
    }
    if (-not $up) { throw 'http.server did not come up' }

    $env:CWM_SERVICE_DRYRUN = '1'
    $env:CWM_NO_OPEN = '1'
    $env:CWM_HOME = Join-Path $sandbox 'cwmhome'
    $env:CWM_INSTALL_DIR = Join-Path $sandbox 'bin'
    $env:CWM_INSTALL_API = "http://127.0.0.1:$port"
    $env:CWM_INSTALL_BASE_URL = "http://127.0.0.1:$port/download"
    $env:CWM_CLAUDE_NO_SEARCH = '1'

    Write-Host "== 1. install.ps1 -File under PowerShell $($PSVersionTable.PSVersion)"
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $out = (& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $repo 'scripts\install.ps1') 2>&1 | Out-String)
    $code = $LASTEXITCODE
    $ErrorActionPreference = $prev
    $out -split "`n" | ForEach-Object { Write-Host "    | $_" }
    $exe = Join-Path $env:CWM_INSTALL_DIR 'cwm.exe'
    Check 'install exits 0' ($code -eq 0)
    Check 'cwm.exe + cwmd.exe installed' ((Test-Path -LiteralPath $exe) -and (Test-Path -LiteralPath (Join-Path $env:CWM_INSTALL_DIR 'cwmd.exe')))
    Check 'checksum OK with the 12-char hash' ($out -match ('checksum OK ' + $sum.Substring(0, 12)))
    Check 'numbered steps + summary box' (($out -match '\[8/8\] Opening the dashboard') -and ($out -match "cwm $tag is installed"))
    Check 'cwm init ran' (Test-Path -LiteralPath (Join-Path $env:CWM_HOME 'config.toml'))
    Check 'service step ran (dry run)' ($out -match 'cwm service install \(dry run')
    Check 'no mojibake in the installer output' (-not ($out -match [regex]::Escape([string][char]0x0393 + [char]0x00A3)))
    Check 'user Path has the bin dir' (([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') -contains $env:CWM_INSTALL_DIR)
    Check 'banner art is scripts/banner.txt verbatim (-File, plain)' (Test-Banner $out)

    Write-Host '== 1b. banner with the colour layout (-File, CWM_INSTALL_FANCY=1)'
    $binMain = $env:CWM_INSTALL_DIR
    $env:CWM_NO_SERVICE = '1'
    $env:CWM_INSTALL_FANCY = '1'
    $env:CWM_INSTALL_DIR = Join-Path $sandbox 'bin-fancy'
    $ErrorActionPreference = 'Continue'
    $fancy = (& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $repo 'scripts\install.ps1') 2>&1 | Out-String)
    $ErrorActionPreference = $prev
    Remove-Item Env:\CWM_INSTALL_FANCY -ErrorAction SilentlyContinue
    Check 'banner art verbatim (-File, colour layout)' (Test-Banner $fancy)

    Write-Host '== 1c. irm | iex from the local server (the tester''s command)'
    Copy-Item -LiteralPath (Join-Path $repo 'scripts\install.ps1') -Destination (Join-Path $sandbox 'srv\install.ps1')
    $env:CWM_INSTALL_DIR = Join-Path $sandbox 'bin-iex'
    $ErrorActionPreference = 'Continue'
    $iex = (& powershell -NoProfile -ExecutionPolicy Bypass -Command "irm http://127.0.0.1:$port/install.ps1 | iex" 2>&1 | Out-String)
    $ErrorActionPreference = $prev
    $iex -split "`n" | Select-Object -First 8 | ForEach-Object { Write-Host "    | $_" }
    Check 'irm | iex: installed' (Test-Path -LiteralPath (Join-Path $env:CWM_INSTALL_DIR 'cwm.exe'))
    Check 'banner art verbatim (irm | iex)' (Test-Banner $iex)
    Remove-Item Env:\CWM_NO_SERVICE -ErrorAction SilentlyContinue
    $env:CWM_INSTALL_DIR = $binMain

    Write-Host '== 2. cwm.exe piped with the OEM code page (the v0.6.0 mojibake)'
    # Pin the console to OEM 437 like the tester's (a runner may differ);
    # without a console at all GetConsoleOutputCP is 0, which is ASCII too.
    $old = [Console]::OutputEncoding
    $haveConsole = $true
    try { [Console]::OutputEncoding = [Text.Encoding]::GetEncoding(437) } catch { $haveConsole = $false }
    try {
        $env:CWM_HOME = Join-Path $sandbox 'cwmhome2'
        $ErrorActionPreference = 'Continue'
        $piped = (& $exe init 2>&1 | Out-String)
        $ErrorActionPreference = $prev
    } finally {
        if ($haveConsole) { [Console]::OutputEncoding = $old }
    }
    Write-Host "    | $piped"
    Check 'piped init output is ASCII "ok", not mojibake' (($piped -match 'ok cwm home initialized') -and -not ($piped -match [regex]::Escape([string][char]0x0393)))
    if ($haveConsole) {
        $env:CWM_GLYPHS = 'unicode'
        try {
            [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false
            $env:CWM_HOME = Join-Path $sandbox 'cwmhome3'
            $ErrorActionPreference = 'Continue'
            $u = (& $exe init 2>&1 | Out-String)
            $ErrorActionPreference = $prev
        } finally {
            [Console]::OutputEncoding = $old
            Remove-Item Env:\CWM_GLYPHS -ErrorAction SilentlyContinue
        }
        Check 'UTF-8 console encoding round-trips the check mark (what install.ps1 sets up)' ($u -match ([string][char]0x2713 + ' cwm home initialized'))
    } else {
        Write-Host 'skip UTF-8 round trip: no console attached'
    }

    Write-Host '== 3. tampered checksum aborts'
    [IO.File]::WriteAllText((Join-Path $dl 'checksums.txt'), ('0000' + $sum.Substring(4) + "  $zipName`n"))
    $env:CWM_INSTALL_DIR = Join-Path $sandbox 'bin-tampered'
    $ErrorActionPreference = 'Continue'
    $out = (& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $repo 'scripts\install.ps1') 2>&1 | Out-String)
    $code = $LASTEXITCODE
    $ErrorActionPreference = $prev
    Check 'tampered: exit 1, nothing installed' (($code -ne 0) -and ($out -match 'checksum mismatch') -and -not (Test-Path -LiteralPath (Join-Path $env:CWM_INSTALL_DIR 'cwm.exe')))

    Write-Host '== 4. -Uninstall'
    $env:CWM_INSTALL_DIR = Join-Path $sandbox 'bin'
    $ErrorActionPreference = 'Continue'
    $out = (& powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $repo 'scripts\install.ps1') -Uninstall 2>&1 | Out-String)
    $code = $LASTEXITCODE
    $ErrorActionPreference = $prev
    Check 'uninstall: exit 0, binaries gone, Path cleaned' (($code -eq 0) -and -not (Test-Path -LiteralPath $exe) -and -not (([Environment]::GetEnvironmentVariable('Path', 'User') -split ';') -contains $env:CWM_INSTALL_DIR))
} finally {
    if ($server) { Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue }
    Remove-Item -LiteralPath $sandbox -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "windows installer harness: $($script:pass) passed, $($script:fail) failed"
if ($script:fail -ne 0) { exit 1 }
