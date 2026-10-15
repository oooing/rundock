param(
    [string]$Go = 'go',
    [string[]]$Packages = @('./...'),
    [string]$Run = '.',
    [switch]$Delivery,
    [string]$LocalPlayRoot = '',
    [switch]$TraceGit,
    [ValidateRange(1,100)][int]$PublisherBatchSize = 30
)
$ErrorActionPreference = 'Stop'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
if (!(Get-Command $Go -ErrorAction SilentlyContinue)) {
    $portable = Join-Path $env:USERPROFILE 'go/bin/go.exe'
    if (!(Test-Path -LiteralPath $portable)) { throw 'Go is not installed or not on PATH' }
    $Go = $portable
}
$output = Join-Path $root ('outputs/acceptance/backend-' + [Guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $output
$log = Join-Path $output 'tests.jsonl'
$keys = @('RUNDOCK_DELIVERY_E2E','RUNDOCK_DELIVERY_EVIDENCE','RUNDOCK_PROJECT_E2E','RUNDOCK_INGLOCAL_ROOT','RUNDOCK_VERIFY_GITHUB','RUNDOCK_READONLY_ROOT','GIT_TRACE2_EVENT')
$prior = @{}
foreach ($key in $keys) { $prior[$key] = [Environment]::GetEnvironmentVariable($key, 'Process') }
$timer = [Diagnostics.Stopwatch]::StartNew()
Push-Location (Join-Path $root 'sidecar')
try {
    # External writes are simulated; real project coverage reads configuration only.
    $env:RUNDOCK_DELIVERY_E2E = if ($Delivery) { '1' } else { '' }
    $env:RUNDOCK_DELIVERY_EVIDENCE = $output
    $env:RUNDOCK_PROJECT_E2E = if ($LocalPlayRoot) { '1' } else { '' }
    $env:RUNDOCK_INGLOCAL_ROOT = $LocalPlayRoot
    $env:RUNDOCK_VERIFY_GITHUB = ''
    $env:RUNDOCK_READONLY_ROOT = ''
    $env:GIT_TRACE2_EVENT = if ($TraceGit) { Join-Path $output 'git-timing.jsonl' } else { '' }
    # Full publisher coverage exceeds even a thirty-minute package budget on
    # Windows. Discover all tests and run bounded, serial batches; never omit
    # a test or turn a timeout into a successful result.
    $batches = @()
    if ($Run -eq '.') {
        $packageList = @(& $Go list @Packages 2> (Join-Path $output 'discovery.log'))
        if ($LASTEXITCODE -ne 0) { throw 'Cannot discover backend packages; see discovery.log' }
        $publisher = @($packageList | Where-Object { $_ -match '/internal/publisher$' })
        $otherPackages = @($packageList | Where-Object { $_ -notmatch '/internal/publisher$' })
        if ($otherPackages.Count) { $batches += @{ packages = $otherPackages; run = '.' } }
        if ($publisher.Count) {
            $listed = @(& $Go test $publisher[0] -list '^Test' 2>> (Join-Path $output 'discovery.log'))
            if ($LASTEXITCODE -ne 0) { throw 'Cannot discover publisher tests; see discovery.log' }
            $names = @($listed | Where-Object { $_ -match '^Test\w+$' } | Sort-Object -Unique)
            if (!$names.Count) { throw 'Publisher test discovery returned no tests' }
            $names | Set-Content -LiteralPath (Join-Path $output 'publisher-tests.txt') -Encoding utf8
            # The fault-injection suites contain many subtests and get their
            # own budget, rather than consuming a normal batch's allowance.
            $deliveryNames = @($names | Where-Object { $_ -in @('TestCloudDispatchE2E','TestReleaseDeliveryE2E') })
            foreach ($name in $deliveryNames) { $batches += @{ packages = $publisher; run = '^' + $name + '$' } }
            $names = @($names | Where-Object { $_ -notin $deliveryNames })
            for ($offset = 0; $offset -lt $names.Count; $offset += $PublisherBatchSize) {
                $end = [Math]::Min($offset + $PublisherBatchSize - 1, $names.Count - 1)
                $batches += @{ packages = $publisher; run = '^(' + ($names[$offset..$end] -join '|') + ')$' }
            }
        }
    } else {
        $batches += @{ packages = $Packages; run = $Run }
    }
    $resultCode = 0
    $batchResults = @()
    foreach ($batch in $batches) {
        $batchPackages = @($batch.packages)
        & $Go test -json -p 1 @batchPackages -run $batch.run -count=1 -timeout=30m *>> $log
        $batchCode = $LASTEXITCODE
        $batchResults += @{ packages = $batchPackages; run = $batch.run; exitCode = $batchCode }
        if ($batchCode -ne 0) { $resultCode = $batchCode }
    }
    $events = @(Get-Content -LiteralPath $log | Where-Object { $_.StartsWith('{') } | ForEach-Object { $_ | ConvertFrom-Json })
    $terminal = @($events | Where-Object { $_.Test -and $_.Action -in @('pass','fail','skip') })
    $summary = [ordered]@{
        exitCode = $resultCode; passed = ($resultCode -eq 0); seconds = $timer.Elapsed.TotalSeconds
        boundary = 'Temporary Git repositories, synthetic packages and simulated GitHub; no real publication'
        batches = $batchResults
        packages = @($events | Where-Object { !$_.Test -and $_.Action -in @('pass','fail','skip') } | Select-Object Package,Action,Elapsed)
        testEvents = @{ pass = @($terminal | Where-Object Action -eq 'pass').Count; fail = @($terminal | Where-Object Action -eq 'fail').Count; skip = @($terminal | Where-Object Action -eq 'skip').Count }
        failures = @($terminal | Where-Object Action -eq 'fail' | Select-Object Package,Test,Elapsed)
        skipped = @($terminal | Where-Object Action -eq 'skip' | Select-Object Package,Test)
        slowest = @($terminal | Sort-Object Elapsed -Descending | Select-Object -First 20 Package,Test,Action,Elapsed)
    }
    $summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $output 'summary.json') -Encoding utf8
    Write-Output (Join-Path $output 'summary.json')
} finally {
    Pop-Location
    foreach ($key in $keys) { [Environment]::SetEnvironmentVariable($key, $prior[$key], 'Process') }
}
exit $resultCode
