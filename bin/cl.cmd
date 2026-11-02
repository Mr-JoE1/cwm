@echo off
rem cl.cmd - cmd.exe wrapper: forwards to cl.ps1 (cmd cannot run .ps1 directly).
rem Kept next to cl.ps1 (repo and installed location), so %~dp0 works in both.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0cl.ps1" %*
exit /b %ERRORLEVEL%
