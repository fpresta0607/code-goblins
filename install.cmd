@echo off
rem install.cmd - runs install.ps1 whatever PowerShell execution policy is set locally;
rem one set by Group Policy still applies.
rem In a clone of Code Goblins: .\install.cmd -Dev
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
exit /b %errorlevel%
