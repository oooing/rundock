[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$TagName,

    [Parameter(Mandatory = $true)]
    [string]$NotesFile,

    [string]$AssetDirectory,

    [switch]$SourceOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($TagName -notmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
    throw "Tag 必须严格使用 vX.Y.Z：$TagName"
}
if (-not (Test-Path -LiteralPath $NotesFile -PathType Leaf)) {
    throw "找不到 Release Notes：$NotesFile"
}
if ([string]::IsNullOrWhiteSpace($env:GH_TOKEN)) {
    throw '缺少 GH_TOKEN'
}
$gh = (Get-Command gh -ErrorAction Stop | Select-Object -First 1).Source

function Invoke-Gh([string[]]$Arguments, [switch]$Capture) {
    if ($Capture) {
        $result = & $gh @Arguments 2>&1 | Out-String
        if ($LASTEXITCODE -ne 0) {
            throw "GitHub CLI 执行失败：gh $($Arguments -join ' ')`n$result"
        }
        return $result.Trim()
    }
    & $gh @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "GitHub CLI 执行失败：gh $($Arguments -join ' ')"
    }
}

function Compare-ReleaseAssets {
    [OutputType([pscustomobject])]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [object[]]$ExpectedAssets,

        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [object[]]$RemoteAssets
    )

    $issues = [System.Collections.Generic.List[string]]::new()
    $remoteByName = [System.Collections.Generic.Dictionary[string, object]]::new(
        [System.StringComparer]::Ordinal
    )

    foreach ($remoteAsset in $RemoteAssets) {
        $name = [string]$remoteAsset.name
        if ([string]::IsNullOrWhiteSpace($name)) {
            continue
        }
        if ($remoteByName.ContainsKey($name)) {
            [void]$issues.Add("远端存在重名产物：$name")
            continue
        }
        $remoteByName.Add($name, $remoteAsset)
    }

    foreach ($expectedAsset in $ExpectedAssets) {
        $name = [string]$expectedAsset.Name
        $expectedSize = [long]$expectedAsset.Size
        if (-not $remoteByName.ContainsKey($name)) {
            [void]$issues.Add("缺少产物：$name")
            continue
        }

        $remoteAsset = $remoteByName[$name]
        $sizeProperty = $remoteAsset.PSObject.Properties['size']
        if ($null -eq $sizeProperty -or $null -eq $sizeProperty.Value) {
            [void]$issues.Add("远端未返回产物大小：$name")
            continue
        }

        try {
            $remoteSize = [Convert]::ToInt64(
                $sizeProperty.Value,
                [Globalization.CultureInfo]::InvariantCulture
            )
        } catch {
            [void]$issues.Add("远端产物大小无效：$name")
            continue
        }
        if ($remoteSize -ne $expectedSize) {
            [void]$issues.Add("产物大小不一致：$name（本地 $expectedSize 字节，远端 $remoteSize 字节）")
        }
    }

    return [pscustomobject]@{
        IsComplete = $issues.Count -eq 0
        Issues = @($issues.ToArray())
    }
}

$expectedAssets = @()
if (-not $SourceOnly) {
    if ([string]::IsNullOrWhiteSpace($AssetDirectory) -or -not (Test-Path -LiteralPath $AssetDirectory -PathType Container)) {
        throw "找不到 Release 产物目录：$AssetDirectory"
    }
    $version = $TagName.Substring(1)
    foreach ($name in @("Launcher_${version}_x64-setup.exe", "Launcher_${version}_x64_en-US.msi", 'SHA256SUMS.txt')) {
        $path = Join-Path $AssetDirectory $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "缺少 Release 产物：$name"
        }
        $file = Get-Item -LiteralPath $path -ErrorAction Stop
        $expectedAssets += [pscustomobject]@{
            Path = $file.FullName
            Name = $file.Name
            Size = [long]$file.Length
        }
    }
}

& git rev-parse --verify --quiet "refs/tags/$TagName" *> $null
if ($LASTEXITCODE -ne 0) {
    throw "检出内容中不存在 Tag：$TagName"
}

$existingText = & $gh release view $TagName --json isDraft,url,assets 2>&1 | Out-String
$viewExitCode = $LASTEXITCODE
$release = $null
if ($viewExitCode -eq 0) {
    $release = $existingText | ConvertFrom-Json
} elseif ($existingText -notmatch '(?i)(release not found|HTTP 404|not found)') {
    throw "无法查询 GitHub Release：$existingText"
}

$title = ".bat启动器管理 $TagName"
if ($null -eq $release) {
    Invoke-Gh @('release', 'create', $TagName, '--draft', '--verify-tag', '--title', $title, '--notes-file', $NotesFile)
    $release = (Invoke-Gh @('release', 'view', $TagName, '--json', 'isDraft,url,assets') -Capture | ConvertFrom-Json)
} elseif (-not [bool]$release.isDraft) {
    $verification = Compare-ReleaseAssets -ExpectedAssets $expectedAssets -RemoteAssets @($release.assets)
    if (-not $verification.IsComplete) {
        throw "release_conflict: 已公开的 Release 产物不完整，自动流程不会覆盖：$($verification.Issues -join '；')"
    }
    Write-Host "[release] 已公开且产物完整：$($release.url)"
    if (-not [string]::IsNullOrWhiteSpace($env:GITHUB_OUTPUT)) {
        "release_url=$($release.url)" | Add-Content -LiteralPath $env:GITHUB_OUTPUT -Encoding utf8
    }
    return
}

# 草稿允许安全重试：先刷新文案，再覆盖同名资产，最后统一公开。
Invoke-Gh @('release', 'edit', $TagName, '--draft', '--title', $title, '--notes-file', $NotesFile, '--verify-tag')
if ($expectedAssets.Count -gt 0) {
    $assetPaths = @($expectedAssets | ForEach-Object { [string]$_.Path })
    Invoke-Gh (@('release', 'upload', $TagName) + $assetPaths + @('--clobber'))
}

$release = (Invoke-Gh @('release', 'view', $TagName, '--json', 'isDraft,url,assets') -Capture | ConvertFrom-Json)
$verification = Compare-ReleaseAssets -ExpectedAssets $expectedAssets -RemoteAssets @($release.assets)
if (-not $verification.IsComplete) {
    throw "release_assets_incomplete: 草稿产物不完整：$($verification.Issues -join '；')"
}

Invoke-Gh @('release', 'edit', $TagName, '--draft=false', '--latest', '--title', $title, '--notes-file', $NotesFile, '--verify-tag')
$url = Invoke-Gh @('release', 'view', $TagName, '--json', 'url', '--jq', '.url') -Capture
Write-Host "[release] 已公开：$url"
if (-not [string]::IsNullOrWhiteSpace($env:GITHUB_OUTPUT)) {
    "release_url=$url" | Add-Content -LiteralPath $env:GITHUB_OUTPUT -Encoding utf8
}
