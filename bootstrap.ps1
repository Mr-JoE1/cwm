# cwm - Claude Workspace Manager installer (Windows). Idempotent: safe to re-run anytime.
# PowerShell 5.1 compatible.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Continue'  # Stop + native stderr redirect misbehaves on PS 5.1

$Src = $PSScriptRoot
$HomeDir = $env:USERPROFILE
$Cfg = Join-Path $HomeDir '.claude-me'
$BinDir = Join-Path $HomeDir 'bin'
$MinVersion = [version]'2.1.234'

Write-Host '== cwm bootstrap (Windows) =='

# 1. claude presence + version (warn only, never abort)
$claudeCmd = Get-Command claude -ErrorAction SilentlyContinue
if ($null -ne $claudeCmd) {
    $verLine = ''
    try { $verLine = @(& claude --version) -join ' ' } catch { }
    $m = [regex]::Match($verLine, '\d+\.\d+\.\d+')
    if ($m.Success) {
        $ver = [version]$m.Value
        if ($ver -lt $MinVersion) {
            Write-Warning "claude $($m.Value) < 2.1.234 - CLAUDE_CODE_PROJECT_DIR_NAME pinning needs >= 2.1.234"
        } else {
            Write-Host "claude $($m.Value) ok"
        }
    }
} else {
    Write-Warning "'claude' not found on PATH (install Claude Code, then re-run bootstrap)"
}

# 2. directory skeleton
foreach ($d in @((Join-Path $Cfg 'hooks'), (Join-Path $Cfg 'logs'), (Join-Path $HomeDir 'repos'), $BinDir)) {
    New-Item -ItemType Directory -Force -Path $d | Out-Null
}

# 3. settings.json is machine-specific: generate once, never clobber
$settings = Join-Path $Cfg 'settings.json'
if (Test-Path -LiteralPath $settings) {
    Write-Host "settings.json exists - keeping it (hint: compare with $(Join-Path $Src 'templates\settings.windows.json.tmpl'))"
} else {
    $tmpl = Get-Content -LiteralPath (Join-Path $Src 'templates\settings.windows.json.tmpl') -Raw
    $content = $tmpl.Replace('{{HOME}}', $HomeDir)
    # PS 5.1 Set-Content -Encoding UTF8 writes a BOM, so write via .NET instead
    [System.IO.File]::WriteAllText($settings, $content)
    Write-Host "generated $settings"
}

# 4. hooks are ours: overwrite with the repo versions
Copy-Item -LiteralPath (Join-Path $Src 'hooks\session-start.ps1') -Destination (Join-Path $Cfg 'hooks\session-start.ps1') -Force
Copy-Item -LiteralPath (Join-Path $Src 'hooks\session-end.ps1') -Destination (Join-Path $Cfg 'hooks\session-end.ps1') -Force

# 5. stignore: overwrite (policy is centralized in the kit)
Copy-Item -LiteralPath (Join-Path $Src 'templates\stignore.tmpl') -Destination (Join-Path $Cfg '.stignore') -Force

# 6. launcher (cwm canonical + cmd shim, cl legacy alias, doctor for `cwm doctor`)
Copy-Item -LiteralPath (Join-Path $Src 'bin\cwm.ps1') -Destination (Join-Path $BinDir 'cwm.ps1') -Force
Copy-Item -LiteralPath (Join-Path $Src 'bin\cwm.cmd') -Destination (Join-Path $BinDir 'cwm.cmd') -Force
Copy-Item -LiteralPath (Join-Path $Src 'bin\cl.ps1') -Destination (Join-Path $BinDir 'cl.ps1') -Force
Copy-Item -LiteralPath (Join-Path $Src 'bin\cl.cmd') -Destination (Join-Path $BinDir 'cl.cmd') -Force
Copy-Item -LiteralPath (Join-Path $Src 'bin\doctor.ps1') -Destination (Join-Path $BinDir 'doctor.ps1') -Force

# 7. persist env vars (User scope)
[Environment]::SetEnvironmentVariable('CLAUDE_CONFIG_DIR', $Cfg, 'User')
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }
if (($userPath -split ';') -notcontains $BinDir) {
    $newPath = ($userPath.Trim(';') + ';' + $BinDir).Trim(';')
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
}

# 8. next steps
Write-Host ''
Write-Host 'cwm bootstrap done.'
Write-Host "  workspace : $Cfg"
Write-Host "  launcher  : $BinDir\cwm  (cl stays as a legacy alias)"
Write-Host "  doctor    : $BinDir\doctor.ps1  (run 'cwm doctor' for health checks)"
Write-Host '  open a NEW terminal for PATH/env changes'
Write-Host 'next steps:'
Write-Host '  1. pair Syncthing on the .claude-me folder with your other device'
Write-Host '     (Windows: %USERPROFILE%\.claude-me; Linux: ~/.claude-me)'
Write-Host '  2. run the cwm bootstrap on the other device, then accept the pairing'
Write-Host "  3. 'cwm' lists projects; 'cwm <name>' clones on demand and resumes"
