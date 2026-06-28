@echo off
REM ===================================================================
REM  release.bat — 一键发版
REM  做三件事：
REM    1. 编译最新的 Go 后端到 Tauri binaries 目录
REM    2. cargo tauri build 打包（前端+Rust+安装包）
REM    3. 把安装包拷到 dist/ 目录方便分发
REM  用法：双击，或命令行 scripts\release.bat
REM ===================================================================
chcp 65001 >nul
setlocal

set "CODE_DIR=%~dp0.."
set "BIN_DIR=%CODE_DIR%\src-tauri\binaries"
set "TRIPLE=x86_64-pc-windows-msvc"

REM 配置免安装版 Go 环境
set "GOROOT=%USERPROFILE%\go"
set "GOPATH=%USERPROFILE%\gopath"
set "PATH=%USERPROFILE%\go\bin;%PATH%"

echo ==================================================
echo   Launcher 发版打包
echo ==================================================
echo.

echo [1/3] 编译 Go 后端 -^> binaries\
pushd "%CODE_DIR%\sidecar"
go build -o "%BIN_DIR%\launcher-sidecar-%TRIPLE%.exe" ./cmd/launcher-sidecar
if errorlevel 1 (
    echo [错误] 后端编译失败
    popd
    pause
    exit /b 1
)
popd
echo       完成。
echo.

echo [2/3] cargo tauri build ^(会编译 Rust release，约 2-5 分钟^) ...
cd /d "%CODE_DIR%"
cargo tauri build
if errorlevel 1 (
    echo [错误] Tauri 打包失败
    pause
    exit /b 1
)
echo       完成。
echo.

echo [3/3] 拷贝安装包到 dist\ ...
if not exist "%CODE_DIR%\dist" mkdir "%CODE_DIR%\dist"
set "NSIS_DIR=%CODE_DIR%\src-tauri\target\release\bundle\nsis"
set "MSI_DIR=%CODE_DIR%\src-tauri\target\release\bundle\msi"
if exist "%NSIS_DIR%\*.exe" copy /Y "%NSIS_DIR%\*.exe" "%CODE_DIR%\dist\" >nul
if exist "%MSI_DIR%\*.msi" copy /Y "%MSI_DIR%\*.msi" "%CODE_DIR%\dist\" >nul
echo       完成。
echo.

echo ==================================================
echo   发版完成！安装包在:
echo     %CODE_DIR%\dist\
echo ==================================================
echo.
dir /b "%CODE_DIR%\dist\*.exe" "%CODE_DIR%\dist\*.msi" 2>nul
echo.
pause
endlocal
