param([string]$Go = 'go', [string]$MakeNSIS = "$env:LOCALAPPDATA\tauri\NSIS\makensis.exe")
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$runRoot = Join-Path $repo ('.tmp/installer-shutdown-' + [Guid]::NewGuid().ToString('N'))
$installed = Join-Path $runRoot 'installed app 中文'
$other = Join-Path $runRoot 'other app'
New-Item -ItemType Directory -Path $installed, $other -Force | Out-Null
$processes = @()
function Check([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Run-Hidden([string]$File, [string]$Arguments) {
    $p = Start-Process -FilePath $File -ArgumentList $Arguments -WindowStyle Hidden -PassThru -Wait
    Check ($p.ExitCode -eq 0) "Failed: $File (exit $($p.ExitCode))"
}
function Start-Backend([string]$Directory, [switch]$Legacy) {
    $ready = Join-Path $Directory 'ready'
    if (Test-Path -LiteralPath $ready) { Remove-Item -LiteralPath $ready }
    $args = '-ready "' + $ready + '"'
    if ($Legacy) { $args += ' -legacy' }
    $p = Start-Process -FilePath (Join-Path $Directory 'launcher-sidecar.exe') -ArgumentList $args -WindowStyle Hidden -PassThru
    for ($i = 0; $i -lt 50 -and -not (Test-Path -LiteralPath $ready); $i++) { Start-Sleep -Milliseconds 100 }
    Check (Test-Path -LiteralPath $ready) 'Test backend did not start'
    return $p
}
try {
    @'
package main
import("flag";"net";"net/http";"os";"time")
func main(){ready:=flag.String("ready","","ready file");legacy:=flag.Bool("legacy",false,"");flag.Parse();done:=make(chan bool,1);mux:=http.NewServeMux();mux.HandleFunc("/api/health",func(w http.ResponseWriter,r *http.Request){w.Header().Set("Content-Type","application/json");w.Write([]byte(`{"status":"ok"}`))});mux.HandleFunc("/api/desktop/shutdown",func(w http.ResponseWriter,r *http.Request){if *legacy {http.NotFound(w,r);return};w.Header().Set("Content-Type","application/json");w.Write([]byte(`{"shuttingDown":true}`));os.WriteFile(*ready+".graceful",[]byte("ok"),0600);done<-true});ln,_:=net.Listen("tcp","127.0.0.1:0");os.WriteFile(*ready,[]byte(ln.Addr().String()),0600);go http.Serve(ln,mux);<-done;time.Sleep(200*time.Millisecond)}
'@ | Set-Content -LiteralPath (Join-Path $runRoot 'backend.go') -Encoding utf8
    & $Go build -o (Join-Path $installed 'launcher-sidecar.exe') (Join-Path $runRoot 'backend.go')
    Check ($LASTEXITCODE -eq 0) 'Could not build test backend'
    Copy-Item -LiteralPath (Join-Path $installed 'launcher-sidecar.exe') -Destination $other
    Copy-Item -LiteralPath (Join-Path $installed 'launcher-sidecar.exe') -Destination (Join-Path $installed 'launcher-platform.exe')
    $config = Join-Path $installed 'project-data.json'
    '{"keep":"all projects"}' | Set-Content -LiteralPath $config
    $before = (Get-FileHash -LiteralPath $config).Hash
    $unrelated = Start-Backend $other
    $processes += $unrelated
    $target = Start-Backend $installed
    $processes += $target
    $main = Start-Process -FilePath (Join-Path $installed 'launcher-platform.exe') -ArgumentList ('-ready "' + (Join-Path $installed 'main-ready') + '"') -WindowStyle Hidden -PassThru
    $processes += $main
    $hook = Join-Path $repo 'src-tauri/windows/installer-hooks.nsh'
    $setup = Join-Path $runRoot 'setup.exe'
    @"
Unicode true
RequestExecutionLevel user
SilentInstall silent
SilentUnInstall silent
!include "LogicLib.nsh"
!include "$hook"
Name "RunDock installer lifecycle test"
OutFile "$setup"
InstallDir "$installed"
Section
!insertmacro NSIS_HOOK_PREINSTALL
SetOutPath `$INSTDIR
File /oname=launcher-sidecar.exe "$other\launcher-sidecar.exe"
WriteUninstaller "`$INSTDIR\test-uninstall.exe"
SectionEnd
Section "Uninstall"
!insertmacro NSIS_HOOK_PREUNINSTALL
Delete "`$INSTDIR\launcher-sidecar.exe"
SectionEnd
"@ | Set-Content -LiteralPath (Join-Path $runRoot 'test.nsi') -Encoding utf8
    & $MakeNSIS /V2 /INPUTCHARSET UTF8 (Join-Path $runRoot 'test.nsi')
    Check ($LASTEXITCODE -eq 0) 'NSIS hook compilation failed'
    Run-Hidden $setup '/S'
    Check ($target.WaitForExit(10000)) 'Install did not stop its backend'
    Check ($main.WaitForExit(10000)) 'Install did not stop its main window process'
    Check (Test-Path -LiteralPath (Join-Path $installed 'ready.graceful')) 'Modern backend did not receive graceful shutdown'
    Check (-not $unrelated.HasExited) 'Another installation was stopped'
    Check ((Get-FileHash -LiteralPath $config).Hash -eq $before) 'Project data changed'
    Write-Output 'PASS: NSIS installation replaces a running backend; other installation and data remain intact.'

    Remove-Item -LiteralPath (Join-Path $installed 'ready.graceful')
    $legacy = Start-Backend $installed -Legacy
    $processes += $legacy
    Run-Hidden (Join-Path $installed 'test-uninstall.exe') ('/S _?=' + $installed)
    Check ($legacy.WaitForExit(10000)) 'Uninstall did not stop legacy backend'
    Check (-not (Test-Path -LiteralPath (Join-Path $installed 'launcher-sidecar.exe'))) 'Uninstall did not release/remove the executable'
    Check (-not $unrelated.HasExited) 'Uninstall stopped another installation'
    Check ((Get-FileHash -LiteralPath $config).Hash -eq $before) 'Uninstall changed project data'
    Write-Output 'PASS: NSIS uninstall stops a legacy backend and removes its executable without removing project data.'

    # A lock from an unrelated program must fail safely, not be ignored.
    Copy-Item -LiteralPath (Join-Path $other 'launcher-sidecar.exe') -Destination $installed
    $lock = [IO.File]::Open((Join-Path $installed 'launcher-sidecar.exe'), [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::None)
    try {
        $blocked = Start-Process -FilePath $setup -ArgumentList '/S' -WindowStyle Hidden -PassThru -Wait
        Check ($blocked.ExitCode -ne 0) 'Locked installation should fail before copying files'
        Check (-not $unrelated.HasExited) 'A lock caused an unrelated process to be terminated'
        Check ((Get-FileHash -LiteralPath $config).Hash -eq $before) 'Blocked install changed project data'
    } finally { $lock.Dispose() }
    Write-Output 'PASS: unrelated file locks stop setup with a failure code and preserve data.'
} finally {
    foreach ($p in $processes) { if (-not $p.HasExited) { Stop-Process -Id $p.Id -ErrorAction SilentlyContinue }; $p.Dispose() }
}
