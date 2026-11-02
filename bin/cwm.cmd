@echo off
rem cwm.cmd - cmd.exe wrapper: forwards to cwm.ps1 (cmd cannot run .ps1 directly).
rem Kept next to cwm.ps1 (repo and installed location), so %~dp0 works in both.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0cwm.ps1" %*
exit /b %ERRORLEVEL%
