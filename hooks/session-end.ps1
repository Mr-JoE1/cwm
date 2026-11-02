# SessionEnd hook: WIP auto-push. If the tree is dirty, commit everything and
# push to sync/<hostname> on origin so the other device can pick up in-flight
# work. Contract: never fails the session - always exit 0, errors go to the log.
# PowerShell 5.1 compatible. No StrictMode on purpose: never-fail contract.
$ErrorActionPreference = 'SilentlyContinue'

$cfg = $env:CLAUDE_CONFIG_DIR
if (-not $cfg) { $cfg = Join-Path $env:USERPROFILE '.claude-me' }
$logDir = Join-Path $cfg 'logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$log = Join-Path $logDir 'wip.log'

function Write-Log([string]$Message) {
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    Add-Content -LiteralPath $log -Value "$stamp $Message" -ErrorAction SilentlyContinue
}

try {
    $url = (@(git remote get-url --push origin 2>$null) -join '').Trim()
    if (-not $url) { Write-Log "skip: cwd is not a git repo with an origin remote ($PWD)"; exit 0 }
    $dirty = @(git status --porcelain 2>$null)
    if ($dirty.Count -eq 0) { Write-Log "skip: tree is clean ($PWD)"; exit 0 }

    $raw = $null
    try { $raw = [System.Net.Dns]::GetHostName() } catch { }
    if (-not $raw) { $raw = $env:COMPUTERNAME }
    if (-not $raw) { $raw = 'unknown' }
    $hostId = ((($raw -split '\.')[0]) -replace '[^A-Za-z0-9-]', '-').Trim('-')
    if (-not $hostId) { $hostId = 'unknown' }

    git add -A 2>> $log
    if ($LASTEXITCODE -ne 0) { Write-Log "error: git add failed ($PWD)"; exit 0 }
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    # -c identity overrides so commits never fail on unconfigured machines
    git -c user.name='claude-sync' -c user.email='claude-sync@localhost' commit -m "wip: $hostId $stamp" --no-verify 2>> $log
    if ($LASTEXITCODE -ne 0) { Write-Log "error: commit failed ($PWD)"; exit 0 }
    git push origin "HEAD:refs/heads/sync/$hostId" 2>> $log
    if ($LASTEXITCODE -ne 0) { Write-Log "error: push failed for sync/$hostId (non-force; WIP commit left local)"; exit 0 }
    Write-Log "ok: pushed HEAD to sync/$hostId ($url)"
} catch {
    Write-Log "error: $($_.Exception.Message)"
}
exit 0
