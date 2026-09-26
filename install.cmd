@echo off
setlocal
rem install.cmd - runs install.ps1 whatever PowerShell execution policy is set locally;
rem one set by Group Policy still applies. Windows PowerShell gets its own module
rem path: one inherited from PowerShell 7 lists modules it cannot load.
rem In a clone of Code Goblins: .\install.cmd -Dev
set "PSModulePath="
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
exit /b %errorlevel%
