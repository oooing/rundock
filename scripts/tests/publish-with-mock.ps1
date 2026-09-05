param([string]$TagName, [string]$NotesFile, [string]$AssetDirectory, [switch]$SourceOnly)

# This wrapper cannot call a real GitHub CLI or access an account.
function Get-Command([string]$Name) {
    if ($Name -ne 'gh') { throw "Unexpected command lookup: $Name" }
    return [pscustomobject]@{ Source = (Join-Path $PSScriptRoot 'mock-gh.ps1') }
}
$env:GH_TOKEN = 'isolated-test-placeholder'
& (Join-Path $PSScriptRoot '../publish-github-release.ps1') `
    -TagName $TagName -NotesFile $NotesFile -AssetDirectory $AssetDirectory -SourceOnly:$SourceOnly
