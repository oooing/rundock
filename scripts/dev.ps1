param(
    [switch]$NoBrowser,
    [switch]$SmokeTest,
    [string]$DataDir = (Join-Path $env:APPDATA 'launcher-sidecar-dev')
)

$ErrorActionPreference = 'Stop'
$codeDir = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$backendPort = 17655
# These defaults agree with Vite, Tauri and dev.bat readiness declarations.
# No fixed range is universally safe; check the actual bind before compiling.
$frontendPort = 17656
$children = @()
$exitCode = 0
$logFiles = @()

function Assert-PortFree([int]$Port) {
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $Port)
    $listener.ExclusiveAddressUse = $true
    try { $listener.Start() }
    catch {
        $socketError = $_.Exception
        while ($socketError.InnerException) { $socketError = $socketError.InnerException }
        if ($socketError -is [Net.Sockets.SocketException]) {
            if ($socketError.SocketErrorCode -eq [Net.Sockets.SocketError]::AddressAlreadyInUse) {
                throw "Development port $Port is in use by a running application. Close that application or the existing development instance, then retry."
            }
            if ($socketError.SocketErrorCode -eq [Net.Sockets.SocketError]::AccessDenied) {
                $ranges = & netsh.exe int ipv4 show excludedportrange protocol=tcp 2>$null
                foreach ($range in $ranges) {
                    if ($range -match '^\s+(\d+)\s+(\d+)\s*\*?\s*$' -and $Port -ge [int]$Matches[1] -and $Port -le [int]$Matches[2]) {
                        throw "Development port $Port is reserved by Windows. Closing applications cannot release a system-reserved port."
                    }
                }
                throw "Windows denied access to development port $Port. It may be reserved or restricted; no occupying application has been identified."
            }
        }
        throw "Cannot bind development port ${Port}: $($socketError.Message)"
    }
    finally { $listener.Stop() }
}

function Start-DevProcess($File, $Arguments, $Directory, $Label) {
    $outFile = Join-Path $logDir "$Label.stdout.log"
    $errFile = Join-Path $logDir "$Label.stderr.log"
    $script:logFiles += @($outFile, $errFile)
    $process = Start-Process -FilePath $File -ArgumentList $Arguments -WorkingDirectory $Directory `
        -WindowStyle Hidden -PassThru -RedirectStandardOutput $outFile -RedirectStandardError $errFile
    $script:children += $process
    return $process
}

function Wait-Ready($Process, $Url, [bool]$Backend) {
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        if ($Process.HasExited) { throw "Process exited before ready: $Url (exit $($Process.ExitCode))" }
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 2
            if ($response.StatusCode -eq 200) {
                if (-not $Backend) { return }
                $health = $response.Content | ConvertFrom-Json
                if ($health.apiVersion -eq '2' -and $health.capabilities -eq 'release-v2') { return }
            }
        } catch { }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Startup timed out: $Url"
}

try {
    # Check before compilation; never stop an unrelated port owner.
    Assert-PortFree $backendPort
    Assert-PortFree $frontendPort
    Add-Type -Path (Join-Path $PSScriptRoot 'dev-job.cs')
    [RunDockDevJob]::Attach()
    $DataDir = [IO.Path]::GetFullPath($DataDir)
    $productionDir = [IO.Path]::GetFullPath((Join-Path $env:APPDATA 'launcher-sidecar'))
    if ($DataDir.TrimEnd('\') -ieq $productionDir.TrimEnd('\')) {
        throw 'Development must not use the installed application data directory.'
    }
    $env:LAUNCHER_DATA_DIR = $DataDir
    $env:LAUNCHER_PORT = "$backendPort"
    $env:LAUNCHER_DEV = '1'
    # This independent launcher owns the backend; it survives worker restarts.
    $env:LAUNCHER_SUPERVISOR_PID = "$PID"
    $env:VITE_LAUNCHER_BASE = "http://127.0.0.1:$backendPort"
    $logDir = Join-Path $DataDir ('dev-logs\' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $logDir -Force | Out-Null
    $tmpDir = Join-Path $codeDir 'sidecar\.tmp'
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
    $sidecarExe = Join-Path $tmpDir 'launcher-sidecar-v2-dev.exe'

    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        $portableGo = Join-Path $env:USERPROFILE 'go'
        if (Test-Path (Join-Path $portableGo 'bin\go.exe')) {
            $env:GOROOT = $portableGo
            $env:PATH = (Join-Path $portableGo 'bin') + ';' + $env:PATH
        }
    }
    $go = (Get-Command go -ErrorAction Stop).Source
    if (-not $env:GOPATH) { $env:GOPATH = Join-Path $env:USERPROFILE 'gopath' }
    $node = (Get-Command node -ErrorAction Stop).Source
    $vite = Join-Path $codeDir 'node_modules\vite\bin\vite.js'
    if (-not (Test-Path $vite)) { throw 'Dependencies missing. Run npm ci in the code directory first.' }

    Write-Host "RunDock development | data: $DataDir"
    Write-Host '[1/3] Building development backend...'
    Push-Location (Join-Path $codeDir 'sidecar')
    try {
        & $go build -o $sidecarExe ./cmd/launcher-sidecar
        if ($LASTEXITCODE -ne 0) { throw "Backend build failed (exit $LASTEXITCODE)." }
    } finally { Pop-Location }

    Write-Host '[2/3] Starting isolated backend...'
    $backend = Start-DevProcess $sidecarExe @('-port', "$backendPort") $codeDir 'backend'
    Wait-Ready $backend "http://127.0.0.1:$backendPort/api/health" $true
    Write-Host '[3/3] Starting development frontend...'
    $frontend = Start-DevProcess $node @(('"' + $vite + '"'), '--host', '127.0.0.1', '--port', "$frontendPort", '--strictPort') $codeDir 'frontend'
    Wait-Ready $frontend "http://127.0.0.1:$frontendPort" $false
    Write-Host "Ready: http://127.0.0.1:$frontendPort"
    Write-Host "Backend: http://127.0.0.1:$backendPort | logs: $logDir"
    if (-not $NoBrowser -and -not $SmokeTest) { Start-Process "http://127.0.0.1:$frontendPort" }
    if (-not $SmokeTest) {
        $generation = 0
        while (-not $frontend.HasExited) {
            if ($backend.HasExited) {
                if ($backend.ExitCode -ne 75) { break }
                Write-Host 'Restart requested: rebuilding backend; frontend stays open.'
                Assert-PortFree $backendPort
                Push-Location (Join-Path $codeDir 'sidecar')
                try {
                    & $go build -o $sidecarExe ./cmd/launcher-sidecar
                    if ($LASTEXITCODE -ne 0) { throw "Backend rebuild failed (exit $LASTEXITCODE). See launcher logs." }
                } finally { Pop-Location }
                $generation++
                $backend = Start-DevProcess $sidecarExe @('-port', "$backendPort") $codeDir "backend-$generation"
                Wait-Ready $backend "http://127.0.0.1:$backendPort/api/health" $true
            }
            Start-Sleep -Milliseconds 500
        }
        throw 'A development service exited. Stopping the paired service.'
    }
} catch {
    Write-Host "[ERROR] $($_.Exception.Message)"
    foreach ($file in $logFiles) {
        if (Test-Path $file) { Get-Content -LiteralPath $file -Encoding UTF8 -Tail 20 }
    }
    $exitCode = 1
} finally {
    # Only processes created by this invocation, including their children.
    foreach ($child in $children) {
        if (-not $child.HasExited) { & taskkill.exe /PID $child.Id /T /F 2>&1 | Out-Null }
    }
}
exit $exitCode
