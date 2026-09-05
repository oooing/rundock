[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$TagName,

    [string]$OutputDirectory = (Join-Path (Get-Location) "release-plan"),

    [switch]$DryRun,

    [switch]$SourceOnly,

    [switch]$ValidateLauncherVersion
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# Git tag bodies are UTF-8. A redirected Windows process can otherwise decode
# native output using the machine's legacy code page and corrupt Chinese notes.
$OutputEncoding = [Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = $OutputEncoding

function Fail-Plan([string]$Message) {
    throw "release_plan_invalid: $Message"
}

function ConvertFrom-Base64Url([string]$Value) {
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-Plan "发布计划内容为空"
    }
    $base64 = $Value.Replace('-', '+').Replace('_', '/')
    switch ($base64.Length % 4) {
        0 { }
        2 { $base64 += '==' }
        3 { $base64 += '=' }
        default { Fail-Plan "发布计划不是有效的 Base64URL" }
    }
    try {
        return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($base64))
    } catch {
        Fail-Plan "发布计划不是有效的 Base64URL"
    }
}

function Assert-ExactProperties($Object, [string[]]$Allowed, [string]$Location) {
    $names = @($Object.PSObject.Properties.Name)
    foreach ($name in $names) {
        if ($Allowed -notcontains $name) {
            Fail-Plan "$Location 包含未知字段：$name"
        }
    }
    foreach ($name in $Allowed) {
        if ($names -notcontains $name) {
            Fail-Plan "$Location 缺少字段：$name"
        }
    }
}

function Assert-Boolean($Value, [string]$Location) {
    if ($Value -isnot [bool]) {
        Fail-Plan "$Location 必须是布尔值"
    }
}

function Assert-LauncherVersion([string]$ExpectedVersion) {
    $root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
    $package = Get-Content -LiteralPath (Join-Path $root 'package.json') -Raw | ConvertFrom-Json
    $tauri = Get-Content -LiteralPath (Join-Path $root 'src-tauri/tauri.conf.json') -Raw | ConvertFrom-Json
    $cargoText = Get-Content -LiteralPath (Join-Path $root 'src-tauri/Cargo.toml') -Raw
    $cargoMatch = [regex]::Match($cargoText, '(?ms)^\[package\].*?^version\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"')
    if (-not $cargoMatch.Success) {
        Fail-Plan '无法读取 src-tauri/Cargo.toml 的 package.version'
    }

    $node = Get-Command node -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $node) {
        Fail-Plan '版本校验需要 Node.js'
    }
    $packageLockPath = Join-Path $root 'package-lock.json'
    $readLockScript = "const fs=require('fs');const p=JSON.parse(fs.readFileSync(process.argv[1],'utf8'));const root=p.packages&&p.packages[''];process.stdout.write(JSON.stringify({version:p.version,rootVersion:(root&&root.version)||''}));"
    $lockText = & $node.Source -e $readLockScript $packageLockPath 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) {
        Fail-Plan "无法读取 package-lock.json：$lockText"
    }
    $lock = $lockText | ConvertFrom-Json

    $versions = [ordered]@{
        'package.json' = [string]$package.version
        'package-lock.json' = [string]$lock.version
        'package-lock.json#packages-root' = [string]$lock.rootVersion
        'src-tauri/tauri.conf.json' = [string]$tauri.version
        'src-tauri/Cargo.toml' = $cargoMatch.Groups[1].Value
    }
    foreach ($entry in $versions.GetEnumerator()) {
        if ($entry.Value -cne $ExpectedVersion) {
            Fail-Plan "版本不一致：$($entry.Key)=$($entry.Value)，Tag=v$ExpectedVersion"
        }
    }
    if (-not (Test-Path -LiteralPath (Join-Path $root 'src-tauri/Cargo.lock') -PathType Leaf)) {
        Fail-Plan '缺少 src-tauri/Cargo.lock；发布构建必须锁定 Rust 依赖'
    }
}

if ($TagName -notmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
    Fail-Plan "Tag 必须严格使用 vX.Y.Z：$TagName"
}
$targetVersion = $TagName.Substring(1)
if ($ValidateLauncherVersion) {
    Assert-LauncherVersion $targetVersion
}

$metadataPresent = $false
$tagMessage = "Release $TagName"
$plan = $null

if ($DryRun) {
    $targets = @()
    if (-not $SourceOnly) {
        $targets = @([pscustomobject]@{
            id = "windows"
            kind = "desktop"
            build = $false
            package = $false
            publish = $true
            deploy = $false
        })
    }
    $plan = [pscustomobject]@{
        schemaVersion = 1
        tagName = $TagName
        targetVersion = $targetVersion
        versionGroupId = "product"
        pushRemote = $false
        publishesRelease = $false
        targets = $targets
    }
} else {
    & git rev-parse --verify --quiet "refs/tags/$TagName" *> $null
    if ($LASTEXITCODE -ne 0) {
        Fail-Plan "本地检出内容中不存在 Tag：$TagName"
    }

    $tagType = (& git cat-file -t "refs/tags/$TagName" 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) {
        Fail-Plan "无法读取 Tag：$TagName"
    }
    if ($tagType -ne "tag") {
        Fail-Plan "发布 Tag 必须是 annotated tag，当前类型为：$tagType"
    }
    $tagMessage = (& git for-each-ref "refs/tags/$TagName" '--format=%(contents)' | Out-String).Trim()

    $markerPattern = '<!--\s*launcher-release-plan:([A-Za-z0-9_-]+)\s*-->'
    $markerMatches = [regex]::Matches($tagMessage, $markerPattern)
    if ($markerMatches.Count -gt 1) {
        Fail-Plan "Tag 中只能包含一个发布计划"
    }
    if ($tagMessage.Contains('launcher-release-plan') -and $markerMatches.Count -ne 1) {
        Fail-Plan "Tag 中的发布计划标记格式无效"
    }

    if ($markerMatches.Count -eq 0) {
        Fail-Plan "Tag 缺少启动器确认的发布计划，请重新执行发布预检后再发布"
    }
    $metadataPresent = $true
    $json = ConvertFrom-Base64Url $markerMatches[0].Groups[1].Value
    try {
        $plan = $json | ConvertFrom-Json
    } catch {
        Fail-Plan "Tag 中的发布计划不是有效 JSON"
    }

    Assert-ExactProperties $plan @('schemaVersion', 'tagName', 'targetVersion', 'versionGroupId', 'pushRemote', 'publishesRelease', 'targets') '发布计划'
    if ($plan.schemaVersion -isnot [int] -and $plan.schemaVersion -isnot [long]) {
        Fail-Plan "schemaVersion 必须是整数"
    }
    if ([long]$plan.schemaVersion -ne 1) {
        Fail-Plan "不支持的 schemaVersion：$($plan.schemaVersion)"
    }
    if ($plan.tagName -isnot [string] -or $plan.tagName -cne $TagName) {
        Fail-Plan "计划 Tag 与触发 Tag 不一致"
    }
    if ($plan.targetVersion -isnot [string] -or $plan.targetVersion -cne $targetVersion) {
        Fail-Plan "计划版本与触发 Tag 不一致"
    }
    if ($plan.versionGroupId -isnot [string] -or [string]::IsNullOrWhiteSpace($plan.versionGroupId)) {
        Fail-Plan "versionGroupId 不能为空"
    }
    Assert-Boolean $plan.pushRemote 'pushRemote'
    Assert-Boolean $plan.publishesRelease 'publishesRelease'
    if ($plan.publishesRelease -and -not $plan.pushRemote) {
        Fail-Plan "publishesRelease=true 时 pushRemote 必须为 true"
    }
    if ($null -eq $plan.targets -or $plan.targets -isnot [array]) {
        Fail-Plan "targets 必须是数组；仅发布源码请显式使用 []"
    }

    $targetIds = @{}
    $targetIndex = 0
    foreach ($target in @($plan.targets)) {
        Assert-ExactProperties $target @('id', 'kind', 'build', 'package', 'publish', 'deploy') "targets[$targetIndex]"
        if ($target.id -isnot [string] -or [string]::IsNullOrWhiteSpace($target.id)) {
            Fail-Plan "targets[$targetIndex].id 不能为空"
        }
        if ($target.kind -isnot [string] -or [string]::IsNullOrWhiteSpace($target.kind)) {
            Fail-Plan "targets[$targetIndex].kind 不能为空"
        }
        foreach ($field in @('build', 'package', 'publish', 'deploy')) {
            Assert-Boolean $target.$field "targets[$targetIndex].$field"
        }
        if (-not ($target.build -or $target.package -or $target.publish -or $target.deploy)) {
            Fail-Plan "targets[$targetIndex] 没有选择任何操作"
        }
        $id = ([string]$target.id).ToLowerInvariant()
        $kind = ([string]$target.kind).ToLowerInvariant()
        $isWindowsTarget = $id -eq 'windows' -or $id -match '(^|[-_/])windows$'
        if (-not ($isWindowsTarget -and $kind -eq 'desktop')) {
            Fail-Plan "Launcher 自动发布仅支持 Windows 目标；不支持：$($target.id)"
        }
        if ($targetIds.ContainsKey($id)) {
            Fail-Plan "发布目标重复：$($target.id)"
        }
        $targetIds[$id] = $true
        $targetIndex += 1
    }
}

$selectedTargets = @($plan.targets)
$isSourceOnly = $selectedTargets.Count -eq 0
$buildWindows = $false
foreach ($target in $selectedTargets) {
    $id = ([string]$target.id).ToLowerInvariant()
    $kind = ([string]$target.kind).ToLowerInvariant()
    $isWindowsTarget = $id -eq 'windows' -or $id -match '(^|[-_/])windows$'
    if ($isWindowsTarget -and $kind -eq 'desktop' -and ($target.build -or $target.package -or $target.publish -or $target.deploy)) {
        $buildWindows = $true
    }
}

$notes = $tagMessage
$notes = [regex]::Replace($notes, '<!--\s*launcher-release-plan:[A-Za-z0-9_-]+\s*-->', '').Trim()
$releaseHeader = '^\s*Release\s+' + [regex]::Escape($TagName) + '\s*(?:\r?\n)?'
$notes = [regex]::Replace($notes, $releaseHeader, '').Trim()
if ([string]::IsNullOrWhiteSpace($notes)) {
    $notes = "此版本未填写更新说明。"
}

New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$utf8NoBom = New-Object Text.UTF8Encoding($false)
$notesPath = Join-Path $OutputDirectory 'release-notes.md'
[IO.File]::WriteAllText($notesPath, $notes + [Environment]::NewLine, $utf8NoBom)

$summary = [ordered]@{
    schemaVersion = 1
    tagName = $TagName
    targetVersion = $targetVersion
    versionGroupId = [string]$plan.versionGroupId
    metadataPresent = $metadataPresent
    dryRun = [bool]$DryRun
    sourceOnly = $isSourceOnly
    buildWindows = $buildWindows
    pushRemote = [bool]$plan.pushRemote
    publishesRelease = if ($DryRun) { $false } else { [bool]$plan.publishesRelease }
    targetIds = @($selectedTargets | ForEach-Object { [string]$_.id })
}
$summaryPath = Join-Path $OutputDirectory 'release-plan.json'
[IO.File]::WriteAllText($summaryPath, ($summary | ConvertTo-Json -Depth 10) + [Environment]::NewLine, $utf8NoBom)

if (-not [string]::IsNullOrWhiteSpace($env:GITHUB_OUTPUT)) {
    @(
        "tag_name=$TagName"
        "target_version=$targetVersion"
        "metadata_present=$($metadataPresent.ToString().ToLowerInvariant())"
        "source_only=$($isSourceOnly.ToString().ToLowerInvariant())"
        "build_windows=$($buildWindows.ToString().ToLowerInvariant())"
        "publishes_release=$(([bool]$summary.publishesRelease).ToString().ToLowerInvariant())"
    ) | Add-Content -LiteralPath $env:GITHUB_OUTPUT -Encoding utf8
}

$summary | ConvertTo-Json -Depth 10
