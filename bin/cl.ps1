# cl.ps1 - legacy alias for cwm (kept one major version, then deprecated; see
# docs/PLAN.md D8). Forwards everything to cwm.ps1 in the same directory.
# PowerShell 5.1 compatible.
$target = Join-Path $PSScriptRoot 'cwm.ps1'
& powershell -NoProfile -ExecutionPolicy Bypass -File $target @args
exit $LASTEXITCODE
