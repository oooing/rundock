param([Parameter(ValueFromRemainingArguments = $true)][string[]]$CliArguments)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $env:RELEASE_TEST_STATE) { throw 'Missing isolated release-test state' }
$state = Get-Content -LiteralPath $env:RELEASE_TEST_STATE -Raw | ConvertFrom-Json
$state.calls = @($state.calls) + ,$CliArguments
$global:LASTEXITCODE = 0
$result = $null

if ($CliArguments[0] -ne 'release') { throw 'Only release CLI calls are allowed in this test' }
switch ($CliArguments[1]) {
    'view' {
        if ($null -eq $state.release) {
            $global:LASTEXITCODE = 1
            $result = 'release not found'
        } elseif ($CliArguments -contains '--jq') {
            $result = $state.release.url
        } else {
            $result = $state.release | ConvertTo-Json -Depth 10 -Compress
        }
    }
    'create' {
        if ($null -ne $state.release) { throw 'Duplicate release creation' }
        $state.release = [pscustomobject]@{ isDraft = $true; url = 'https://example.invalid/release'; assets = @() }
    }
    'edit' {
        if ($CliArguments -contains '--draft=false') { $state.release.isDraft = $false }
    }
    'upload' {
        if (-not $state.release.isDraft) { throw 'Attempted to modify a public release' }
        $state.release.assets = @($CliArguments | Select-Object -Skip 3 | Where-Object { $_ -ne '--clobber' } | ForEach-Object {
            $file = Get-Item -LiteralPath $_
            [pscustomobject]@{ name = $file.Name; size = $file.Length }
        })
    }
    default { throw "Unexpected release action: $($CliArguments[1])" }
}
$state | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $env:RELEASE_TEST_STATE -Encoding utf8
if ($null -ne $result) { Write-Output $result }
