param([string]$Backend = 'http://127.0.0.1:17655', [string]$AppId = 'd87363129d2c76a9', [Nullable[double]]$InitialObservedSeconds = $null)
$ErrorActionPreference = 'Stop'
$codeRoot = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$output = Join-Path $codeRoot 'outputs'
$dataDir = Join-Path $output ('acceptance/preflight-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
function Request($Method, $Url, $Body = $null) {
    $options = @{Method=$Method; Uri=$Url; TimeoutSec=120}
    if ($null -ne $Body) { $options.ContentType='application/json'; $options.Body=($Body | ConvertTo-Json -Depth 15 -Compress) }
    $result = Invoke-RestMethod @options
    if ($result -is [string]) { $result = $result | ConvertFrom-Json -AsHashtable }
    return $result
}
function ClassificationSignature($Preflight) {
    return (@($Preflight.classifications | ForEach-Object {
        '{0}|{1}|{2}|{3}|{4}|{5}' -f $_.path,$_.oldPath,$_.status,$_.category,$_.selectedDefault,$_.contentFingerprint
    }) -join "`n")
}
$liveHealth = Request Get "$Backend/api/health"
$liveApp = Request Get "$Backend/api/apps/$AppId"
$profile = Request Get "$Backend/api/apps/$AppId/release-profile"
$historyBefore = Request Get "$Backend/api/apps/$AppId/releases?limit=10"
$before = @()
for ($index=0; $index -lt 2; $index++) {
    $watch = [Diagnostics.Stopwatch]::StartNew()
    $original = Request Post "$Backend/api/apps/$AppId/release/preflight?remote=false"
    $before += $watch.Elapsed.TotalSeconds
}
if (-not $original.statusFingerprint) { throw 'Missing baseline fingerprint' }
$probe = $null
$priorDataDir = $env:LAUNCHER_DATA_DIR
$priorDev = $env:LAUNCHER_DEV
$priorSupervisor = $env:LAUNCHER_SUPERVISOR_PID
try {
    $env:LAUNCHER_DATA_DIR=$dataDir
    $env:LAUNCHER_DEV='1'
    $env:LAUNCHER_SUPERVISOR_PID=$null
    $probe = Start-Process -FilePath (Join-Path $codeRoot 'sidecar/.tmp/launcher-sidecar-preflight.exe') -ArgumentList '-addr','127.0.0.1:0' -WorkingDirectory $codeRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $dataDir 'stdout.log') -RedirectStandardError (Join-Path $dataDir 'stderr.log')
    $portFile = Join-Path $dataDir 'sidecar.port'
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    while (-not (Test-Path $portFile)) {
        if ($probe.HasExited -or [DateTime]::UtcNow -gt $deadline) { throw 'Isolated backend did not start' }
        Start-Sleep -Milliseconds 100
    }
    $base = 'http://127.0.0.1:' + (Get-Content $portFile -Raw).Trim()
    $app = Request Post "$base/api/apps" @{name='Read-only preflight acceptance';cwd=$liveApp.cwd;entryScript=$liveApp.entryScript;adapterType='batch'}
    if (-not $app.id) { throw 'Isolated project was not created' }
    $body = @{}
    foreach ($key in @('buildMode','syncPolicy','remoteName','versionStrategy','preReleaseCommand','createTag','versionMode')) { $body[$key]=$profile.$key }
    $null = Request Patch "$base/api/apps/$($app.id)/release-profile" $body
    $after = @()
    for ($index=0; $index -lt 3; $index++) {
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $optimized = Request Post "$base/api/apps/$($app.id)/release/preflight?remote=false"
        $after += $watch.Elapsed.TotalSeconds
        if ($optimized.statusFingerprint -cne $original.statusFingerprint -or $optimized.headSha -cne $original.headSha) { throw 'Source changed or snapshot format drifted' }
        if ((ClassificationSignature $optimized) -cne (ClassificationSignature $original)) { throw 'File choices or content fingerprints changed' }
        if (($optimized.blockingIssues | ConvertTo-Json -Depth 10 -Compress) -cne ($original.blockingIssues | ConvertTo-Json -Depth 10 -Compress)) { throw 'Blocking results changed' }
    }
    $historyAfter = Request Get "$Backend/api/apps/$AppId/releases?limit=10"
    if (($historyBefore | ConvertTo-Json -Depth 20 -Compress) -cne ($historyAfter | ConvertTo-Json -Depth 20 -Compress)) { throw 'Live release history changed during measurement' }
    $report = [ordered]@{
        measuredAt=[DateTime]::UtcNow.ToString('o'); appId=$AppId; files=@($original.changes).Count
        initialObservedSeconds=$InitialObservedSeconds; baselineSeconds=$before; optimizedSeconds=$after
        sourceFingerprintUnchanged=$true; classificationsUnchanged=$true; blockersUnchanged=$true
        releaseHistoryUnchanged=$true; liveInstanceUnchanged=((Request Get "$Backend/api/health").instanceId -eq $liveHealth.instanceId)
        isolatedBackend=$true; constructedCandidate=$false; builtProject=$false; published=$false
    }
    $report | ConvertTo-Json -Depth 10 | Set-Content (Join-Path $output 'inglocalplay-preflight-performance-20261013.json') -Encoding utf8
    $report | ConvertTo-Json -Depth 10
} finally {
    $env:LAUNCHER_DATA_DIR=$priorDataDir
    $env:LAUNCHER_DEV=$priorDev
    $env:LAUNCHER_SUPERVISOR_PID=$priorSupervisor
    if ($probe -and -not $probe.HasExited) { Stop-Process -Id $probe.Id -ErrorAction SilentlyContinue }
}
