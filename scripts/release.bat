@echo off
setlocal EnableExtensions

set "BUILD_SCRIPT=%~dp0release-build.ps1"
if not exist "%BUILD_SCRIPT%" (
    echo [ERROR] Missing %BUILD_SCRIPT%
    exit /b 1
)

echo ==================================================
echo   RunDock release build
echo ==================================================
echo.

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%BUILD_SCRIPT%"
set "BUILD_EXIT=%ERRORLEVEL%"

if not "%BUILD_EXIT%"=="0" (
    echo.
    echo [ERROR] Release build failed with exit code %BUILD_EXIT%.
) else (
    echo.
    echo [OK] Installers and SHA256SUMS.txt are in dist\
)

if not defined CI pause
exit /b %BUILD_EXIT%
