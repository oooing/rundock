@echo off
REM ===================================================================
REM  build-sidecar.bat
REM  把 Go 后端编译到 Tauri 要求的 binaries 目录（带 target-triple 后缀）。
REM  仅 Tauri 桌面应用模式需要，且只在改了 Go 代码后重跑。
REM  详见 README.md
REM ===================================================================
chcp 65001 >nul
setlocal

set "CODE_DIR=%~dp0.."
set "SIDECAR_DIR=%CODE_DIR%\sidecar"
set "BIN_DIR=%CODE_DIR%\src-tauri\binaries"
set "TRIPLE=x86_64-pc-windows-msvc"
set "OUT=%BIN_DIR%\launcher-sidecar-%TRIPLE%.exe"

REM 配置免安装版 Go 环境变量（必须）
set "GOROOT=%USERPROFILE%\go"
set "GOPATH=%USERPROFILE%\gopath"
set "PATH=%USERPROFILE%\go\bin;%PATH%"

if not exist "%BIN_DIR%" mkdir "%BIN_DIR%"

echo [build] 编译 Go 后端 -> %OUT%
cd /d "%SIDECAR_DIR%"
go build -o "%OUT%" ./cmd/launcher-sidecar
if errorlevel 1 (
    echo [build] 失败，请检查上面的错误
    pause
    exit /b 1
)
echo [build] 完成: %OUT%
echo.
echo 下一步：cd .. ^&^& cargo tauri dev  （启动桌面应用）
timeout /t 3 /nobreak >nul
endlocal
