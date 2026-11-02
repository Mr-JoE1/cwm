# SessionStart hook: remember "this machine worked on repo X at origin Y" in
# repos.tsv so `cl <name>` can clone it on demand on another device.
# Contract: this hook must never fail the session - always exit 0.
# PowerShell 5.1 compatible. No StrictMode on purpose: never-fail contract.
$ErrorActionPreference = 'SilentlyContinue'

$cfg = $env:CLAUDE_CONFIG_DIR
if (-not $cfg) { $cfg = Join-Path $env:USERPROFILE '.claude-me' }
$logDir = Join-Path $cfg 'logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$log = Join-Path $logDir 'session-start.log'

function Write-Log([string]$Message) {
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    Add-Content -LiteralPath $log -Value "$stamp $Message" -ErrorAction SilentlyContinue
}

try {
    $url = (@(git remote get-url --push origin 2>$null) -join '').Trim()
    if (-not $url) { Write-Log "skip: cwd is not a git repo with an origin remote ($PWD)"; exit 0 }
    $name = Split-Path -Leaf (Get-Location).Path
    $tsv = Join-Path $cfg 'repos.tsv'
    $lines = @()
    if (Test-Path -LiteralPath $tsv) { $lines = @(Get-Content -LiteralPath $tsv) }
    $lines = @($lines + "$name`t$url" | Where-Object { $_ } | Sort-Object -Unique)
    Set-Content -LiteralPath $tsv -Value $lines -ErrorAction Stop
} catch {
    Write-Log "error: $($_.Exception.Message)"
}
exit 0
