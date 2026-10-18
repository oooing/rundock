# One-time migration of the old running backend. Local installation only.
$ErrorActionPreference = 'Stop'
$codeRoot = (Get-Location).Path
$build = Get-Content -LiteralPath (Join-Path $codeRoot 'outputs\acceptance\latest-silent-update-build.json') -Raw | ConvertFrom-Json
$result = Get-Content -LiteralPath (Join-Path $build.evidence 'build-result.json') -Raw | ConvertFrom-Json
if ($result.exitCode -ne 0 -or $result.commit -ne $build.commit) { throw 'Build is not complete' }
$packageName = "RunDock_$($build.version)_x64-setup.exe"
$package = Join-Path $build.evidence "packages\$packageName"
$sha = (Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash.ToLowerInvariant()
if ($sha -ne ($result.artifacts | Where-Object name -eq $packageName).sha256) { throw 'Installer hash mismatch' }
$data = Join-Path $env:APPDATA 'launcher-sidecar'
$pendingFile = Join-Path $data 'desktop-update.json'
$beforePending = Get-Content -LiteralPath $pendingFile -Raw -Encoding UTF8
$pending = $beforePending | ConvertFrom-Json
if ($pending.phase -ne 'finished') { throw 'An update is still in progress' }
$installed = Join-Path $env:LOCALAPPDATA 'RunDock'
if (!(Test-Path -LiteralPath (Join-Path $installed 'uninstall.exe'))) { throw 'Existing NSIS installation required' }
$beforeInstalled = Invoke-RestMethod 'http://127.0.0.1:17654/api/health' -TimeoutSec 5
$beforeDev = Invoke-RestMethod 'http://127.0.0.1:17655/api/health' -TimeoutSec 5
$installerProcess = Start-Process -FilePath $package -ArgumentList '/S','/UPDATE','/P' -WorkingDirectory $installed -WindowStyle Hidden -PassThru
$done = $installerProcess.WaitForExit(180000)
if (!$done) { throw "Installer still running, PID $($installerProcess.Id); do not rerun" }
if ($installerProcess.ExitCode -ne 0) { throw "Installer failed: $($installerProcess.ExitCode)" }
$version = (Get-Item -LiteralPath (Join-Path $installed 'launcher-platform.exe')).VersionInfo.ProductVersion
if ($version -ne $build.version) { throw 'Installed version mismatch' }
$diskHash = (Get-FileHash -LiteralPath (Join-Path $installed 'launcher-sidecar.exe') -Algorithm SHA256).Hash
$expectedHash = (Get-FileHash -LiteralPath (Join-Path $build.evidence 'binaries\launcher-sidecar.exe') -Algorithm SHA256).Hash
if ($diskHash -ne $expectedHash) { throw 'Installed backend hash mismatch' }
if ((Get-Content -LiteralPath $pendingFile -Raw -Encoding UTF8) -cne $beforePending) { throw 'Unresolved recovery intent changed during file installation' }
foreach ($probe in @(@{port=17654; before=$beforeInstalled}, @{port=17655; before=$beforeDev})) {
  $now = Invoke-RestMethod "http://127.0.0.1:$($probe.port)/api/health" -TimeoutSec 5
  if ($now.instanceId -ne $probe.before.instanceId) { throw "Backend $($probe.port) changed during silent installation" }
}
$report = @{version=$version; installer=$package; sha256=$sha; exitCode=$installerProcess.ExitCode; recoveryPreserved=$true; backendsUnchanged=$true; githubPublished=$false}
$report | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $codeRoot 'outputs\acceptance\activation-live-20261018\bootstrap-install.json')
$report | ConvertTo-Json -Compress
