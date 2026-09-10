[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$InstallDirectory)

# Embedded in the installer/uninstaller. Never stop processes by image name
# alone: a development checkout or another installation may run concurrently.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-InstallRoot([string]$Directory) {
    if (-not [IO.Path]::IsPathRooted($Directory)) { throw 'The installation directory must be an absolute path.' }
    $full = [IO.Path]::GetFullPath($Directory).TrimEnd('\', '/')
    if ($full -eq [IO.Path]::GetPathRoot($full).TrimEnd('\', '/')) { throw 'A drive root is not an installation directory.' }
    return $full
}
function Get-InstalledProcesses([string]$Root) {
    $expected = @((Join-Path $Root 'launcher-platform.exe'), (Join-Path $Root 'launcher-sidecar.exe'))
    @(Get-CimInstance Win32_Process -Filter "Name = 'launcher-platform.exe' OR Name = 'launcher-sidecar.exe'" | Where-Object {
        $_.ExecutablePath -and $expected -contains [IO.Path]::GetFullPath($_.ExecutablePath)
    })
}
function Wait-ProcessExit([int]$ProcessID, [int]$Milliseconds) {
    $process = Get-Process -Id $ProcessID -ErrorAction SilentlyContinue
    if ($null -eq $process) { return $true }
    try { return $process.WaitForExit($Milliseconds) } finally { $process.Dispose() }
}
function Stop-ExactProcess($Snapshot, [string]$Root) {
    $processID = [int]$Snapshot.ProcessId
    # Recheck the executable and creation time before terminating (PID reuse).
    $current = @(Get-InstalledProcesses $Root | Where-Object { $_.ProcessId -eq $processID -and $_.CreationDate -eq $Snapshot.CreationDate })
    if (-not $current.Count) { return }
    Stop-Process -Id $processID -Force -ErrorAction Stop
    if (-not (Wait-ProcessExit $processID 10000)) { throw "Process $processID did not exit." }
}
function Request-BackendShutdown($Snapshot) {
    try {
        $ports = @(Get-NetTCPConnection -State Listen -OwningProcess $Snapshot.ProcessId -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -in @('127.0.0.1', '0.0.0.0', '::1', '::') } | Select-Object -ExpandProperty LocalPort -Unique)
        foreach ($port in $ports) {
            try {
                $health = Invoke-RestMethod -Uri "http://127.0.0.1:$port/api/health" -TimeoutSec 2 -Proxy $null
                if ($health.status -ne 'ok') { continue }
                $response = Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:$port/api/desktop/shutdown" -TimeoutSec 35 -Proxy $null
                if ($response.shuttingDown) { return }
            } catch { Write-Verbose "Graceful shutdown on port $port was unavailable: $_" }
        }
    } catch { Write-Verbose "Could not discover the backend port: $_" }
}
function Assert-InstallFilesReleased([string]$Root) {
    foreach ($name in @('launcher-platform.exe', 'launcher-sidecar.exe')) {
        $path = Join-Path $Root $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { continue }
        $released = $false
        for ($attempt = 0; $attempt -lt 20; $attempt++) {
            try {
                $file = [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
                $file.Dispose()
                $released = $true
                break
            } catch { Start-Sleep -Milliseconds 250 }
        }
        if (-not $released) { throw "Cannot replace $path. Another process or file permissions still prevent access. Close RunDock and retry, or run the installer as administrator." }
    }
}

try {
    $installRoot = Resolve-InstallRoot $InstallDirectory
    # Prevent the old window from recreating a background process during setup.
    foreach ($process in @(Get-InstalledProcesses $installRoot | Where-Object { $_.Name -eq 'launcher-platform.exe' })) { Stop-ExactProcess $process $installRoot }
    foreach ($process in @(Get-InstalledProcesses $installRoot | Where-Object { $_.Name -eq 'launcher-sidecar.exe' })) {
        Request-BackendShutdown $process
        if (-not (Wait-ProcessExit ([int]$process.ProcessId) 6000)) { Stop-ExactProcess $process $installRoot }
    }
    if (@(Get-InstalledProcesses $installRoot).Count) { throw 'RunDock restarted during setup. Close it and retry.' }
    Assert-InstallFilesReleased $installRoot
    Write-Output 'RunDock processes have exited and installation files are ready.'
    exit 0
} catch {
    Write-Output "RunDock could not prepare the installation: $($_.Exception.Message)"
    exit 1
}
