@echo off
rem install.cmd - runs install.ps1 whatever the PowerShell execution policy is.
rem In a clone of Code Goblins: .\install.cmd -Dev
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
exit /b %errorlevel%
