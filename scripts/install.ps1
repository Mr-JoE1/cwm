# cwm installer for Windows (SPEC-V0.6 G, D37). Windows PowerShell 5.1+.
#
#   irm https://mr-joe1.github.io/cwm/install.ps1 | iex
#
# With options (irm | iex cannot pass parameters; use env vars or a scriptblock):
#   $env:CWM_NO_SERVICE = '1'; irm https://mr-joe1.github.io/cwm/install.ps1 | iex
#   & ([scriptblock]::Create((irm https://mr-joe1.github.io/cwm/install.ps1))) -Version v0.6.0 -NoService
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
# Test seams (env):  CWM_INSTALL_API, CWM_INSTALL_BASE_URL, CWM_INSTALL_DIR

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

function Say([string]$msg) { Write-Host "cwm-install: $msg" }
function Fail([string]$msg) { throw "cwm-install: error: $msg" }

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

function Add-UserPath([string]$dir) {
    if (-not (Test-InUserPath $dir)) {
        $p = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ($p) { $p = $p.TrimEnd(';') + ';' + $dir } else { $p = $dir }
        [Environment]::SetEnvironmentVariable('Path', $p, 'User')
        Say "added $dir to your user Path (new terminals pick it up)"
    }
    $cur = [string]$env:Path
    if (-not (($cur.Split(';')) -contains $dir)) { $env:Path = (@($dir, $cur) | Where-Object { $_ }) -join ';' }
}

function Remove-UserPath([string]$dir) {
    $p = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $p) { return }
    $kept = @($p.Split(';') | Where-Object { $_ -and ($_.TrimEnd('\') -ine $dir.TrimEnd('\')) })
    $new = $kept -join ';'
    if ($new -ne $p) {
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
        Say "removed $dir from your user Path"
    }
}

# Invoke-Cwm runs cwm with its output on the console and returns only the
# exit code (without Out-Host the output would join the return value).
function Invoke-Cwm([string]$exe, [string[]]$cwmArgs) {
    & $exe @cwmArgs | Out-Host
    return $LASTEXITCODE
}

function Uninstall-Cwm {
    $bin = Get-BinDir
    $exe = Join-Path $bin 'cwm.exe'
    if (Test-Path -LiteralPath $exe) {
        if ((Invoke-Cwm $exe @('service', 'uninstall')) -ne 0) { Say 'service uninstall reported an error (continuing)' }
        $null = Invoke-Cwm $exe @('service', 'stop')
        foreach ($f in @('cwm.exe', 'cwmd.exe', 'cwm.exe.old', 'cwmd.exe.old')) {
            $p = Join-Path $bin $f
            if (Test-Path -LiteralPath $p) { Remove-Item -LiteralPath $p -Force }
        }
        Say "removed cwm from $bin"
    } else {
        Say "no cwm at $exe"
    }
    Remove-UserPath $bin
    Say "your workspace ($(Get-CwmHome)) was kept - delete it yourself if you want it gone"
}

function Install-Cwm([string]$tag, [bool]$skipService, [bool]$skipOpen) {
    # Windows PowerShell 5.1 may still default to TLS 1.0; GitHub needs 1.2.
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch { }

    $arch = Get-Arch
    if (-not $tag) { $tag = Get-LatestTag }
    if (-not $tag.StartsWith('v')) { $tag = "v$tag" }
    if ($tag -notmatch '^v[A-Za-z0-9._+-]+$') { Fail "invalid version '$tag'" }
    $num = $tag.Substring(1)
    $archive = "cwm_${num}_windows_${arch}.zip"
    $base = "https://github.com/$Repo/releases/download"
    if ($env:CWM_INSTALL_BASE_URL) { $base = $env:CWM_INSTALL_BASE_URL }
    $base = "$base/$tag"
    $bin = Get-BinDir

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("cwm-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Say "installing cwm $tag (windows/$arch) into $bin"
        Get-File "$base/$archive" (Join-Path $tmp $archive)
        Get-File "$base/checksums.txt" (Join-Path $tmp 'checksums.txt')

        $want = $null
        foreach ($line in Get-Content -LiteralPath (Join-Path $tmp 'checksums.txt')) {
            $parts = $line.Trim() -split '\s+'
            if ($parts.Count -ge 2 -and ($parts[1].TrimStart('*') -eq $archive)) { $want = $parts[0].ToLowerInvariant(); break }
        }
        if (-not $want) { Fail "$archive is not listed in checksums.txt - refusing to install" }
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $tmp $archive)).Hash.ToLowerInvariant()
        if ($got -ne $want) { Fail "checksum mismatch for $archive (expected $want, got $got) - nothing was installed" }
        Say "checksum OK ($got)"

        $x = Join-Path $tmp 'x'
        Expand-Archive -LiteralPath (Join-Path $tmp $archive) -DestinationPath $x -Force
        if (-not (Test-Path -LiteralPath (Join-Path $x 'cwm.exe'))) { Fail 'archive has no cwm.exe' }

        New-Item -ItemType Directory -Force -Path $bin | Out-Null
        $exe = Join-Path $bin 'cwm.exe'
        if (Test-Path -LiteralPath $exe) {
            # Upgrade: stop the running agent, then move the old binaries
            # aside (a running .exe can be renamed, not overwritten).
            $null = Invoke-Cwm $exe @('service', 'stop')
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
        Say ("installed " + (& $exe --version))
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    Add-UserPath $bin

    if (-not (Test-Path -LiteralPath (Join-Path (Get-CwmHome) 'config.toml'))) {
        if ((Invoke-Cwm $exe @('init')) -ne 0) { Say 'cwm init failed - run it yourself later' }
    }
    if (-not $skipService) {
        if ((Invoke-Cwm $exe @('service', 'install')) -ne 0) { Say "autostart could not be installed - run 'cwm service install' later" }
    }
    if (-not $skipOpen) {
        if ((Invoke-Cwm $exe @('dashboard')) -ne 0) { Say "could not open the dashboard - run 'cwm dashboard'" }
    }
    Say 'done. Next: cwm account add main   (or use the setup wizard in the dashboard)'
}

if ($Uninstall -or $env:CWM_UNINSTALL -eq '1') {
    Uninstall-Cwm
} else {
    $skipService = $NoService.IsPresent -or ($env:CWM_NO_SERVICE -eq '1')
    $skipOpen = $NoOpen.IsPresent -or ($env:CWM_NO_OPEN -eq '1')
    Install-Cwm $Version $skipService $skipOpen
}
