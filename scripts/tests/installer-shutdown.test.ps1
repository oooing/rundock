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
    & $Go -C (Join-Path $repo 'sidecar') build -o (Join-Path $installed 'launcher-sidecar.exe') (Join-Path $PSScriptRoot 'fixtures/installer-owner.go')
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
    $helper = Join-Path $repo 'src-tauri/binaries/rundock-install-helper-x86_64-pc-windows-msvc.exe'
    & $Go -C (Join-Path $repo 'sidecar') build -trimpath -ldflags '-H=windowsgui' -o $helper ./cmd/rundock-install-helper
    Check ($LASTEXITCODE -eq 0) 'Could not build installer helper'
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
File /oname=launcher-platform.exe "$other\launcher-sidecar.exe"
WriteUninstaller "`$INSTDIR\test-uninstall.exe"
!insertmacro NSIS_HOOK_POSTINSTALL
SectionEnd
Section "Uninstall"
!insertmacro NSIS_HOOK_PREUNINSTALL
Delete "`$INSTDIR\launcher-sidecar.exe"
SectionEnd
"@ | Set-Content -LiteralPath (Join-Path $runRoot 'test.nsi') -Encoding utf8
    & $MakeNSIS /V2 /INPUTCHARSET UTF8 (Join-Path $runRoot 'test.nsi')
    Check ($LASTEXITCODE -eq 0) 'NSIS hook compilation failed'
    Run-Hidden $setup '/S'
    Check (-not $target.HasExited) 'Install stopped its backend'
    Check (-not $main.HasExited) 'Install stopped its main window process'
    Check (-not (Test-Path -LiteralPath (Join-Path $installed 'ready.graceful'))) 'Install requested project shutdown'
    $projectReady = Join-Path $installed 'ready.project'
    $projectPort = (Get-Content -LiteralPath $projectReady -Raw).Trim()
    $projectPID = [int](Get-Content -LiteralPath (Join-Path $installed 'ready.project.pid') -Raw)
    Check ((Invoke-RestMethod -Uri "http://$projectPort/api/health" -Proxy $null -TimeoutSec 3).status -eq 'ok') 'Managed project stopped during install'
    Check ($null -ne (Get-Process -Id $projectPID -ErrorAction SilentlyContinue)) 'Managed project PID changed'
    Check (Test-Path -LiteralPath (Join-Path $installed 'launcher-sidecar.exe')) 'New backend was not installed'
    Check (Test-Path -LiteralPath (Join-Path $installed 'launcher-platform.exe')) 'New main program was not installed'
    Check (-not $unrelated.HasExited) 'Another installation was stopped'
    Check ((Get-FileHash -LiteralPath $config).Hash -eq $before) 'Project data changed'
    Write-Output 'PASS: NSIS installs new files while keeping the shell, backend, Job-owned project PID, other installation and data intact.'

    # Reinstall the same version while earlier loaded backups are still alive.
    Run-Hidden $setup '/S'
    Check (-not $target.HasExited -and -not $main.HasExited) 'Reinstall stopped existing processes'
    Check ((Invoke-RestMethod -Uri "http://$projectPort/api/health" -Proxy $null -TimeoutSec 3).status -eq 'ok') 'Reinstall stopped its managed project'
    Write-Output 'PASS: repeated in-place installation also preserves live projects.'

    # Uninstallation still explicitly removes the app and its owned processes.
    $legacy = Start-Backend $installed -Legacy
    $processes += $legacy
    Run-Hidden (Join-Path $installed 'test-uninstall.exe') ('/S _?=' + $installed)
    Check ($legacy.WaitForExit(10000)) 'Uninstall did not stop legacy backend'
    Check ($target.WaitForExit(10000) -and $main.WaitForExit(10000)) 'Uninstall did not stop staged old programs'
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
