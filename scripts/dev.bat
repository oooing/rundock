@echo off
REM ===================================================================
REM  Launcher 开发模式
REM  双击运行：编译后端 -> 起后端(独立窗口) -> 起前端(本窗口) -> 开浏览器
REM  关闭本窗口 或 按 Ctrl+C = 停止前端；后端窗口需单独关。
REM ===================================================================
chcp 65001 >nul
setlocal

set "CODE_DIR=%~dp0.."
set "SIDECAR_DIR=%CODE_DIR%\sidecar"
set "SIDECAR_EXE=%SIDECAR_DIR%\launcher-sidecar-dev.exe"
set "LOG_FILE=%CODE_DIR%\sidecar-dev.log"
set PORT=17654

REM --- 配置免安装版 Go 环境变量 ---
set "GOROOT=%USERPROFILE%\go"
set "GOPATH=%USERPROFILE%\gopath"
set "PATH=%USERPROFILE%\go\bin;%PATH%"

echo ==================================================
echo   Launcher 开发模式
echo ==================================================
echo GOROOT=%GOROOT%
echo.

echo [1/4] 编译后端 ^(增量^) ...
cd /d "%SIDECAR_DIR%"
go build -o "%SIDECAR_EXE%" ./cmd/launcher-sidecar
if errorlevel 1 (
    echo [错误] 编译失败
    pause
    exit /b 1
)
echo       完成。
echo.

echo [2/4] 启动后端 ^(独立窗口, 日志: sidecar-dev.log^) ...
cd /d "%CODE_DIR%"
start "Launcher-Backend" "%SIDECAR_EXE%" -port %PORT%

REM 等待后端就绪
echo       等待后端就绪 ...
set TRIES=0
:WAIT
timeout /t 1 /nobreak >nul
curl -s "http://127.0.0.1:%PORT%/api/health" >nul 2>nul
if %errorlevel%==0 goto READY
set /a TRIES+=1
if %TRIES% lss 25 goto WAIT
echo [警告] 后端未就绪，请检查 Launcher-Backend 窗口
:READY
echo       后端就绪。
echo.

echo [3/4] 8秒后打开浏览器 ...
start "" cmd /c "timeout /t 8 /nobreak >nul & start http://localhost:1420"

echo [4/4] 启动前端 ^(本窗口^) ...
echo   ________________________________________________
echo.
cd /d "%CODE_DIR%"
call npm run dev

REM 前端退出后
echo.
echo 前端已停止。后端窗口(Launcher-Backend)请手动关闭。
timeout /t 2 /nobreak >nul
endlocal
