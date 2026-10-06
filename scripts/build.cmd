@echo off
rem One-shot build: create the signing certificate, sign the driver package,
rem embed it, and build the exe. All messages are printed by the PowerShell
rem script (this file stays ASCII so cmd.exe cannot mangle it).
rem
rem   scripts\build.cmd
rem   scripts\build.cmd -Pfx C:\path\codesign.pfx -PfxPassword secret
rem   scripts\build.cmd -SkipBuild
setlocal
set SCRIPT=%~dp0build-release.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT%" %*
set CODE=%ERRORLEVEL%
if not "%CODE%"=="0" echo build failed with exit code %CODE%
exit /b %CODE%