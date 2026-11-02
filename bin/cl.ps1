# cl.ps1 - private Claude Code workspace launcher (Windows). Mirrors bin/cl.
#
# WHY: sessions must key off the config dir + a pinned project name
# (CLAUDE_CODE_PROJECT_DIR_NAME), not the OS-specific path slug, so Windows and
# Linux share one projects\<name>\ folder. Plain `claude` does not pin the name,
# so always launch through `cl`.
# PowerShell 5.1 compatible.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Continue'  # Stop + native stderr redirect misbehaves on PS 5.1

$ClaudeMe = Join-Path $env:USERPROFILE '.claude-me'
if (-not (Test-Path -LiteralPath $ClaudeMe -PathType Container)) {
    [Console]::Error.WriteLine("error: $ClaudeMe does not exist.")
    [Console]::Error.WriteLine('Run the kit bootstrap.ps1 first, or pair Syncthing so the')
    [Console]::Error.WriteLine('workspace folder is restored, then retry.')
    exit 1
}
$env:CLAUDE_CONFIG_DIR = $ClaudeMe

function Get-HostId {
    $raw = $null
    try { $raw = [System.Net.Dns]::GetHostName() } catch { }
    if (-not $raw) { $raw = $env:COMPUTERNAME }
    if (-not $raw) { $raw = 'unknown' }
    $short = ($raw -split '\.')[0]
    $san = ($short -replace '[^A-Za-z0-9-]', '-').Trim('-')
    if (-not $san) { $san = 'unknown' }
    return $san
}

function Get-RepoUrl([string]$Name) {
    $tsv = Join-Path $ClaudeMe 'repos.tsv'
    if (-not (Test-Path -LiteralPath $tsv)) { return $null }
    foreach ($line in @(Get-Content -LiteralPath $tsv)) {
        if (-not $line) { continue }
        $parts = $line -split "`t", 2
        if ($parts.Count -ge 2 -and $parts[0] -eq $Name -and $parts[1]) { return $parts[1] }
    }
    return $null
}

function Get-KnownNames {
    $tsv = Join-Path $ClaudeMe 'repos.tsv'
    if (-not (Test-Path -LiteralPath $tsv)) { return @() }
    return @(@(Get-Content -LiteralPath $tsv) |
        Where-Object { $_ } |
        ForEach-Object { ($_ -split "`t", 2)[0] } |
        Sort-Object -Unique)
}

function Show-Projects {
    Write-Host "workspace projects ($ClaudeMe\projects):"
    $projectsDir = Join-Path $ClaudeMe 'projects'
    $found = $false
    if (Test-Path -LiteralPath $projectsDir -PathType Container) {
        $dirs = @(Get-ChildItem -LiteralPath $projectsDir -Directory -ErrorAction SilentlyContinue)
        foreach ($d in $dirs) {
            $found = $true
            $sessions = @(Get-ChildItem -LiteralPath $d.FullName -Filter '*.jsonl' -File -ErrorAction SilentlyContinue)
            $last = 'never'
            if ($sessions.Count -gt 0) {
                $newest = $sessions | Sort-Object LastWriteTime -Descending | Select-Object -First 1
                $last = $newest.LastWriteTime.ToString('yyyy-MM-dd HH:mm')
            }
            Write-Host ('{0,-24} {1,4} session(s)  last: {2}' -f $d.Name, $sessions.Count, $last)
        }
    }
    if (-not $found) {
        Write-Host '  (none yet - open a repo via ''cl <name> new'' to create one)'
    }
}

function Show-Usage {
    Write-Host ''
    Write-Host 'usage:'
    Write-Host '  cl                  list workspace projects'
    Write-Host '  cl <name>           clone on demand if missing, then claude --resume (picker)'
    Write-Host '  cl <name> new       fresh session in that repo'
    Write-Host '  cl <name> last      continue the latest session (claude --continue)'
    Write-Host '  cl --wip            run the WIP auto-push now (same as the SessionEnd hook)'
    Write-Host '  cl <other args...>  passed through to claude; project name is pinned only'
    Write-Host '                      when the cwd is inside ~\repos'
}

function Show-SyncHint([string]$RepoDir) {
    # info only: never merge automatically
    try {
        Push-Location -LiteralPath $RepoDir -ErrorAction Stop
        git fetch -q origin 2>$null
        if ($LASTEXITCODE -ne 0) { Pop-Location; return }
        $mine = Get-HostId
        foreach ($ref in @(git for-each-ref --format='%(refname:short)' refs/remotes/origin/sync/ 2>$null)) {
            if (-not $ref) { continue }
            $branch = $ref -replace '^origin/', ''
            if ($branch -eq $mine) { continue }
            $count = 0
            $out = (git rev-list --count "HEAD..$ref" 2>$null) -join ''
            if ($LASTEXITCODE -eq 0) { $count = [int]$out } else { $count = 0 }
            if ($count -gt 0) {
                Write-Host "hint: sync branch $branch has commits not on this branch (run: git pull origin $branch)"
                break
            }
        }
        Pop-Location
    } catch {
        try { Pop-Location } catch { }
    }
}

function Invoke-PassThrough {
    $reposRoot = Join-Path $env:USERPROFILE 'repos'
    $pwdPath = (Get-Location).Path
    $pin = $null
    if ($pwdPath.Length -ge $reposRoot.Length -and
        $pwdPath.StartsWith($reposRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
        $rel = $pwdPath.Substring($reposRoot.Length).TrimStart([char[]]@('\', '/'))
        if ($rel) { $pin = ($rel -split '[\\/]')[0] }
    }
    if ($pin) {
        $env:CLAUDE_CODE_PROJECT_DIR_NAME = $pin
    } else {
        # outside ~\repos: plain claude with just the private config dir
        Remove-Item Env:\CLAUDE_CODE_PROJECT_DIR_NAME -ErrorAction SilentlyContinue
    }
    & claude @args
    exit $LASTEXITCODE
}

function Invoke-Launch([string]$Name, [string]$Mode) {
    $repo = Join-Path $env:USERPROFILE "repos\$Name"
    if (-not (Test-Path -LiteralPath $repo -PathType Container)) {
        $url = Get-RepoUrl $Name
        if (-not $url) {
            [Console]::Error.WriteLine("error: unknown project '$Name' - no $repo and no entry in $(Join-Path $ClaudeMe 'repos.tsv')")
            $known = @(Get-KnownNames)
            if ($known.Count -gt 0) {
                [Console]::Error.WriteLine('known projects:')
                foreach ($k in $known) { [Console]::Error.WriteLine("  $k") }
            }
            exit 1
        }
        Write-Host "cloning $url -> $repo"
        git clone $url $repo
        if ($LASTEXITCODE -ne 0) { [Console]::Error.WriteLine('clone failed'); exit 1 }
    }
    Set-Location -LiteralPath $repo
    $env:CLAUDE_CODE_PROJECT_DIR_NAME = $Name
    Show-SyncHint $repo
    switch ($Mode) {
        'resume' { & claude --resume }
        'new'    { & claude }
        'last'   { & claude --continue }
    }
    exit $LASTEXITCODE
}

if ($args.Count -eq 0) {
    Show-Projects
    Show-Usage
    exit 0
}

$first = [string]$args[0]

if ($first -eq '--wip') {
    $hook = Join-Path $ClaudeMe 'hooks\session-end.ps1'
    if (Test-Path -LiteralPath $hook) {
        & powershell -NoProfile -ExecutionPolicy Bypass -File $hook
        exit 0
    }
    [Console]::Error.WriteLine("error: WIP hook not found at $hook - re-run the kit bootstrap")
    exit 1
}

if ($first.StartsWith('-')) {
    Invoke-PassThrough @args
}

if ($args.Count -eq 1) {
    Invoke-Launch $first 'resume'
}
elseif ($args.Count -eq 2 -and $args[1] -eq 'new') {
    Invoke-Launch $first 'new'
}
elseif ($args.Count -eq 2 -and $args[1] -eq 'last') {
    Invoke-Launch $first 'last'
}
else {
    Invoke-PassThrough @args
}
