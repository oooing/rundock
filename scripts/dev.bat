@echo off
rem rundock:ready http://127.0.0.1:17655/api/health
rem rundock:ready http://127.0.0.1:17656/
rem rundock:open http://127.0.0.1:17656/
setlocal
chcp 65001 >nul
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0dev.ps1" %*
exit /b %errorlevel%
