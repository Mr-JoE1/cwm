# doctor.ps1 - cwm doctor: local health checks for the cwm workspace (Windows).
# WHY local-only by default: doctor must stay fast and testable offline; the
# SSH probe is opt-in via -net (decision D9 in docs/PLAN.md).
# PowerShell 5.1 compatible.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Continue'

$Cfg = Join-Path $env:USERPROFILE '.claude-me'
$MinVersion = [version]'2.1.234'
$Net = $false
if ($args.Count -eq 1 -and [string]$args[0] -eq '-net') {
    $Net = $true
} elseif ($args.Count -eq 1 -and [string]$args[0] -eq '--net') {
    $Net = $true
} elseif ($args.Count -gt 0) {
    [Console]::Error.WriteLine('usage: cwm doctor [--net]')
    exit 1
}

$script:OkCount = 0
$script:WarnCount = 0
$script:FailCount = 0
function Write-Ok([string]$Message)   { $script:OkCount++;   Write-Host "ok:   $Message" }
function Write-Warn([string]$Message) { $script:WarnCount++; Write-Host "warn: $Message" }
function Write-Fail([string]$Message) { $script:FailCount++; Write-Host "FAIL: $Message" }

# 1. workspace dir
if (Test-Path -LiteralPath $Cfg -PathType Container) {
    Write-Ok "workspace dir exists ($Cfg)"
} else {
    Write-Fail "workspace dir missing ($Cfg) - run the cwm bootstrap or pair Syncthing so the folder is restored"
}

# 2. settings.json: present, hooks wired, wired hooks exist.
#    WHY key-grep instead of "valid JSON": no JSON parser dependency; the
#    "hooks" + "SessionStart" keys are what actually matter for wiring.
$settings = Join-Path $Cfg 'settings.json'
if (-not (Test-Path -LiteralPath $settings -PathType Leaf)) {
    Write-Warn 'settings.json missing - hooks not wired (re-run the cwm bootstrap)'
} else {
    $content = [System.IO.File]::ReadAllText($settings)
    if ($content.Contains('"hooks"') -and $content.Contains('"SessionStart"')) {
        Write-Ok 'settings.json wires hooks ("hooks" + "SessionStart" keys present)'
    } else {
        Write-Warn 'settings.json lacks "hooks"/"SessionStart" keys - hooks not wired'
    }
    $wired = $false
    foreach ($m in [regex]::Matches($content, '"[^"]*\\hooks\\[^"]*\.ps1"')) {
        $hp = $m.Value.Trim('"')
        if (-not $hp) { continue }
        $wired = $true
        if (-not (Test-Path -LiteralPath $hp -PathType Leaf)) {
            Write-Fail "hook wired but missing: $hp - re-run the cwm bootstrap"
        }
    }
    if ($wired) { Write-Ok 'wired hook scripts exist' }
}

# 3. hook scripts installed
$hooksDir = Join-Path $Cfg 'hooks'
if ((Test-Path -LiteralPath $hooksDir -PathType Container) -and
    @(Get-ChildItem -LiteralPath $hooksDir -File -ErrorAction SilentlyContinue).Count -gt 0) {
    $n = @(Get-ChildItem -LiteralPath $hooksDir -File).Count
    Write-Ok "hook scripts present in $hooksDir ($n file(s))"
} else {
    Write-Warn "no hook scripts in $hooksDir - re-run the cwm bootstrap"
}

# 4. sync ignore policy
if (Test-Path -LiteralPath (Join-Path $Cfg '.stignore') -PathType Leaf) {
    Write-Ok '.stignore present (sync ignore policy installed)'
} else {
    Write-Warn '.stignore missing - sync not configured (re-run the cwm bootstrap)'
}

# 5. claude presence + version (pinning needs >= 2.1.234)
$claudeCmd = Get-Command claude -ErrorAction SilentlyContinue
if ($null -ne $claudeCmd) {
    $verLine = ''
    try { $verLine = @(& claude --version) -join ' ' } catch { }
    $m = [regex]::Match($verLine, '\d+\.\d+\.\d+')
    if (-not $m.Success) {
        Write-Ok 'claude on PATH (version not parseable)'
    } elseif ([version]$m.Value -ge $MinVersion) {
        Write-Ok "claude $($m.Value) on PATH (>= 2.1.234: project-name pinning supported)"
    } else {
        Write-Warn "claude $($m.Value) < 2.1.234 - project-name pinning unsupported; upgrade to >= 2.1.234"
    }
} else {
    Write-Warn 'claude not on PATH - install Claude Code'
}

# 6. CLAUDE_CONFIG_DIR persisted as a User env var
$userCfg = [Environment]::GetEnvironmentVariable('CLAUDE_CONFIG_DIR', 'User')
if ($userCfg) {
    Write-Ok "CLAUDE_CONFIG_DIR set for the user ($userCfg)"
} else {
    Write-Warn 'CLAUDE_CONFIG_DIR not set as a User environment variable - re-run the cwm bootstrap'
}

# 7. repos.tsv: readable, every non-empty line is name<TAB>url
$tsv = Join-Path $Cfg 'repos.tsv'
if (Test-Path -LiteralPath $tsv -PathType Leaf) {
    $total = 0
    $bad = 0
    foreach ($line in @(Get-Content -LiteralPath $tsv)) {
        if (-not $line) { continue }
        $total++
        if ($line -notmatch '^[^\t]+\t[^\t]+$') { $bad++ }
    }
    if ($bad -gt 0) {
        Write-Warn "repos.tsv has $bad malformed line(s) (expected: name<TAB>url)"
    } else {
        Write-Ok "repos.tsv readable, $total repo(s) recorded"
    }
} else {
    Write-Warn 'repos.tsv not readable - no repos recorded yet (open a repo via cwm to create it)'
}

# 8. session-folder hygiene: dash-prefixed dirs are OS path slugs from
#    unpinned `claude` launches; their sessions will not resume cross-OS.
$projects = Join-Path $Cfg 'projects'
$legacy = @()
if (Test-Path -LiteralPath $projects -PathType Container) {
    $legacy = @(Get-ChildItem -LiteralPath $projects -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name.StartsWith('-') })
}
if ($legacy.Count -gt 0) {
    $names = ($legacy | ForEach-Object { $_.Name }) -join ' '
    Write-Warn "unpinned session folders (resume via cwm to keep sessions cross-OS; these were created by plain claude): $names"
} else {
    Write-Ok 'no unpinned (dash-prefixed) session folders'
}

# 9. WIP log tail
$wipLog = Join-Path $Cfg 'logs\wip.log'
if (Test-Path -LiteralPath $wipLog -PathType Leaf) {
    Write-Ok 'WIP log tail (last 3 lines of logs\wip.log):'
    foreach ($line in @(Get-Content -LiteralPath $wipLog -Tail 3)) {
        Write-Host "      $line"
    }
} else {
    Write-Ok 'no WIP log yet (no sync/<host> pushes recorded)'
}

# 10. --net only: SSH probe. Never runs in default mode (D9).
if ($Net) {
    $sshOut = ''
    try { $sshOut = @(ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com 2>&1) -join ' ' } catch { }
    if ($sshOut -match 'successfully authenticated') {
        Write-Ok 'ssh git@github.com: successfully authenticated'
    } else {
        Write-Warn "ssh git@github.com failed - check your SSH key ($sshOut)"
    }
}

Write-Host "doctor: $($script:OkCount) ok, $($script:WarnCount) warn, $($script:FailCount) fail"
if ($script:FailCount -gt 0) { exit 1 }
exit 0
