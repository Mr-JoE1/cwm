@echo off
rem cl.cmd - legacy alias wrapper: forwards to cwm.ps1 (cl is a legacy alias
rem for cwm; see docs/PLAN.md D8). Kept next to cwm.ps1 so %~dp0 works.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0cwm.ps1" %*
exit /b %ERRORLEVEL%
