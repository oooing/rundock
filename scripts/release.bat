@echo off
setlocal EnableExtensions

set "CODE_DIR=%~dp0.."
set "BIN_DIR=%CODE_DIR%\src-tauri\binaries"
set "TRIPLE=x86_64-pc-windows-msvc"
set "DIST_DIR=%CODE_DIR%\dist"
set "NSIS_DIR=%CODE_DIR%\src-tauri\target\release\bundle\nsis"
set "MSI_DIR=%CODE_DIR%\src-tauri\target\release\bundle\msi"
set "GOCACHE=%CODE_DIR%\.gocache"

echo ==================================================
echo   Launcher release build
echo ==================================================
echo.

echo [0/3] Checking tools...

go version >nul 2>nul
if errorlevel 1 (
    if exist "%USERPROFILE%\go\bin\go.exe" (
        set "PATH=%USERPROFILE%\go\bin;%PATH%"
    ) else if exist "C:\Program Files\Go\bin\go.exe" (
        set "PATH=C:\Program Files\Go\bin;%PATH%"
    )
)

go version >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Go was not found. Install Go or add go.exe to PATH.
    pause
    exit /b 1
)

cargo --version >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Cargo was not found. Install Rust or add cargo.exe to PATH.
    pause
    exit /b 1
)

call npm.cmd --version >nul 2>nul
if errorlevel 1 (
    echo [ERROR] npm was not found. Install Node.js or add npm to PATH.
    pause
    exit /b 1
)

cargo tauri --version >nul 2>nul
if errorlevel 1 (
    echo [ERROR] Tauri CLI was not found. Run: cargo install tauri-cli --version ^2
    pause
    exit /b 1
)

if not exist "%BIN_DIR%" mkdir "%BIN_DIR%"

echo       OK
echo.

REM Remove old sidecar output to avoid "already exists and is not an object file".
REM A previous interrupted build leaves a corrupt exe, or the file may be locked,
REM either of which makes `go build -o` refuse to overwrite.
set "SIDECAR_OUT=%BIN_DIR%\launcher-sidecar-%TRIPLE%.exe"
if exist "%SIDECAR_OUT%" (
    attrib -r "%SIDECAR_OUT%" >nul 2>nul
    del /f /q "%SIDECAR_OUT%" >nul 2>nul
    if exist "%SIDECAR_OUT%" (
        echo [ERROR] Cannot delete old %SIDECAR_OUT%
        echo        It may be locked by a running sidecar / Tauri / desktop app. Close them and retry.
        pause
        exit /b 1
    )
)

echo [1/3] Building Go sidecar...
pushd "%CODE_DIR%\sidecar"
go build -o "%SIDECAR_OUT%" ./cmd/launcher-sidecar
if errorlevel 1 (
    echo [ERROR] Go sidecar build failed.
    popd
    pause
    exit /b 1
)
popd
echo       OK
echo.

echo [2/3] Building Tauri installer...
cd /d "%CODE_DIR%"
cargo tauri build
if errorlevel 1 (
    echo [ERROR] Tauri build failed.
    pause
    exit /b 1
)
echo       OK
echo.

echo [3/3] Copying installers to dist...
if not exist "%DIST_DIR%" mkdir "%DIST_DIR%"
if exist "%NSIS_DIR%\*.exe" copy /Y "%NSIS_DIR%\*.exe" "%DIST_DIR%\" >nul
if exist "%MSI_DIR%\*.msi" copy /Y "%MSI_DIR%\*.msi" "%DIST_DIR%\" >nul
echo       OK
echo.

echo ==================================================
echo   Release build complete. Installers:
echo   %DIST_DIR%\
echo ==================================================
echo.
dir /b "%DIST_DIR%\*.exe" "%DIST_DIR%\*.msi" 2>nul
echo.
pause
endlocal
