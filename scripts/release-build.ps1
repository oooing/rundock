[CmdletBinding()]
param(
    [string]$TagName,
    [string]$OutputDirectory,
    [switch]$InstallDependencies,
    [switch]$SkipTests,
    [switch]$ValidateOnly,
    [switch]$RequireSigned
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$codeDirectory = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $codeDirectory 'dist'
} elseif (-not [IO.Path]::IsPathRooted($OutputDirectory)) {
    $OutputDirectory = Join-Path $codeDirectory $OutputDirectory
}

function Invoke-Checked([string]$FilePath, [string[]]$Arguments, [string]$WorkingDirectory = $codeDirectory) {
    Push-Location $WorkingDirectory
    try {
        & $FilePath @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "命令执行失败（$LASTEXITCODE）：$FilePath $($Arguments -join ' ')"
        }
    } finally {
        Pop-Location
    }
}

function Resolve-Tool([string]$Name, [string[]]$Fallbacks = @()) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $command) {
        return $command.Source
    }
    foreach ($candidate in $Fallbacks) {
        if (-not [string]::IsNullOrWhiteSpace($candidate) -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    throw "缺少构建工具：$Name"
}

function Get-NpmInvocation {
    $node = Resolve-Tool 'node.exe'
    $npmCli = Join-Path (Split-Path -Parent $node) 'node_modules/npm/bin/npm-cli.js'
    if (Test-Path -LiteralPath $npmCli -PathType Leaf) {
        return @{ File = $node; Prefix = @($npmCli) }
    }
    return @{ File = (Resolve-Tool 'npm.cmd'); Prefix = @() }
}

function Invoke-Npm([string[]]$Arguments) {
    $npm = Get-NpmInvocation
    Invoke-Checked $npm.File (@($npm.Prefix) + $Arguments)
}

function Assert-WorkspaceChild([string]$Path) {
    $fullPath = [IO.Path]::GetFullPath($Path)
    $workspacePrefix = $codeDirectory.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    if (-not $fullPath.StartsWith($workspacePrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "构建路径超出项目目录：$fullPath"
    }
    return $fullPath
}

function Read-Versions {
    $package = Get-Content -LiteralPath (Join-Path $codeDirectory 'package.json') -Raw | ConvertFrom-Json
    $node = Resolve-Tool 'node.exe'
    $packageLockPath = Join-Path $codeDirectory 'package-lock.json'
    $readLockScript = "const fs=require('fs');const p=JSON.parse(fs.readFileSync(process.argv[1],'utf8'));const root=p.packages&&p.packages[''];process.stdout.write(JSON.stringify({version:p.version,rootVersion:(root&&root.version)||''}));"
    $packageLockText = & $node -e $readLockScript $packageLockPath 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) {
        throw "无法读取 package-lock.json：$packageLockText"
    }
    $packageLock = $packageLockText | ConvertFrom-Json
    $tauri = Get-Content -LiteralPath (Join-Path $codeDirectory 'src-tauri/tauri.conf.json') -Raw | ConvertFrom-Json
    $cargoText = Get-Content -LiteralPath (Join-Path $codeDirectory 'src-tauri/Cargo.toml') -Raw
    $cargoMatch = [regex]::Match($cargoText, '(?ms)^\[package\].*?^version\s*=\s*"([0-9]+\.[0-9]+\.[0-9]+)"')
    if (-not $cargoMatch.Success) {
        throw '无法读取 src-tauri/Cargo.toml 的 package.version'
    }
    return [ordered]@{
        package = [string]$package.version
        packageLock = [string]$packageLock.version
        packageLockRoot = [string]$packageLock.rootVersion
        tauri = [string]$tauri.version
        cargo = $cargoMatch.Groups[1].Value
    }
}

$versions = Read-Versions
$version = [string]$versions.package
if ([string]::IsNullOrWhiteSpace($TagName)) {
    $TagName = "v$version"
}
if ($TagName -notmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
    throw "Tag 必须严格使用 vX.Y.Z：$TagName"
}
$tagVersion = $TagName.Substring(1)
foreach ($entry in $versions.GetEnumerator()) {
    if ($entry.Value -cne $tagVersion) {
        throw "版本不一致：$($entry.Key)=$($entry.Value)，Tag=$TagName"
    }
}

$cargoLock = Join-Path $codeDirectory 'src-tauri/Cargo.lock'
if (-not (Test-Path -LiteralPath $cargoLock -PathType Leaf)) {
    throw '缺少 src-tauri/Cargo.lock；发布构建必须锁定 Rust 依赖'
}

Write-Host "[release] 版本校验通过：$TagName"
if ($ValidateOnly) {
    return
}

$go = Resolve-Tool 'go.exe' @(
    (Join-Path $env:USERPROFILE 'go/bin/go.exe'),
    'C:\Program Files\Go\bin\go.exe'
)
$null = Resolve-Tool 'cargo.exe'
$temporaryRoot = Assert-WorkspaceChild (Join-Path $codeDirectory '.tmp/release-build')
New-Item -ItemType Directory -Force -Path $temporaryRoot | Out-Null
$env:GOCACHE = Assert-WorkspaceChild (Join-Path $temporaryRoot 'go-cache')
New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null

if ($InstallDependencies) {
    Write-Host '[release] npm ci'
    Invoke-Npm @('ci')
}

if (-not $SkipTests) {
    Write-Host '[release] 前端类型检查'
    Invoke-Npm @('run', 'type-check')
    Write-Host '[release] Go 测试'
    Invoke-Checked $go @('test', '-count=1', '-timeout=15m', './...') (Join-Path $codeDirectory 'sidecar')
}

$temporarySidecar = Join-Path $temporaryRoot ("launcher-sidecar-{0}.exe" -f [Guid]::NewGuid().ToString('N'))
$tauriBinary = Join-Path $codeDirectory 'src-tauri/binaries/launcher-sidecar-x86_64-pc-windows-msvc.exe'
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $tauriBinary) | Out-Null

try {
    Write-Host '[release] 编译 Go sidecar'
    Invoke-Checked $go @('build', '-trimpath', '-o', $temporarySidecar, './cmd/launcher-sidecar') (Join-Path $codeDirectory 'sidecar')

    Write-Host '[release] sidecar 健康检查'
    $smokeData = Join-Path $temporaryRoot ("appdata-{0}" -f [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $smokeData | Out-Null
    $port = Get-Random -Minimum 20000 -Maximum 45000
    $psi = [Diagnostics.ProcessStartInfo]::new()
    $psi.FileName = $temporarySidecar
    $psi.Arguments = "-port $port"
    $psi.WorkingDirectory = $temporaryRoot
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.WindowStyle = [Diagnostics.ProcessWindowStyle]::Hidden
    $psi.EnvironmentVariables['APPDATA'] = $smokeData
    $process = [Diagnostics.Process]::Start($psi)
    try {
        $healthy = $false
        for ($attempt = 0; $attempt -lt 40; $attempt += 1) {
            Start-Sleep -Milliseconds 250
            try {
                $response = Invoke-RestMethod -Uri "http://127.0.0.1:$port/api/health" -TimeoutSec 1
                if ($response.status -eq 'ok' -and $response.capabilities -eq 'release-v2') {
                    $healthy = $true
                    break
                }
            } catch { }
        }
        if (-not $healthy) {
            throw 'sidecar /api/health 未返回 release-v2'
        }
    } finally {
        if ($null -ne $process -and -not $process.HasExited) {
            $process.Kill()
            $process.WaitForExit()
        }
    }

    Copy-Item -LiteralPath $temporarySidecar -Destination $tauriBinary -Force

    # 只清理本次 Tauri bundle 输出目录，避免误把旧版本安装包上传到新 Release。
    foreach ($bundleName in @('nsis', 'msi')) {
        $bundleDirectory = Assert-WorkspaceChild (Join-Path $codeDirectory "src-tauri/target/release/bundle/$bundleName")
        if (Test-Path -LiteralPath $bundleDirectory) {
            Remove-Item -LiteralPath $bundleDirectory -Recurse -Force
        }
    }

    Write-Host '[release] 构建 Tauri NSIS + MSI'
    Invoke-Npm @('run', 'tauri', '--', 'build', '--ci', '--bundles', 'nsis,msi', '--', '--locked')

    $expected = @(
        (Join-Path $codeDirectory "src-tauri/target/release/bundle/nsis/Launcher_${tagVersion}_x64-setup.exe"),
        (Join-Path $codeDirectory "src-tauri/target/release/bundle/msi/Launcher_${tagVersion}_x64_en-US.msi")
    )
    foreach ($path in $expected) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "缺少预期安装包：$path"
        }
    }

    New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
    $assetPaths = @()
    foreach ($path in $expected) {
        $destination = Join-Path $OutputDirectory (Split-Path -Leaf $path)
        Copy-Item -LiteralPath $path -Destination $destination -Force
        $assetPaths += $destination
    }

    $checksumLines = @()
    $assets = @()
    foreach ($path in ($assetPaths | Sort-Object)) {
        $file = Get-Item -LiteralPath $path
        $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        $signature = Get-AuthenticodeSignature -LiteralPath $path
        if ($RequireSigned -and $signature.Status -ne 'Valid') {
            throw "安装包未通过 Windows 代码签名校验：$($file.Name)（$($signature.Status)）"
        }
        $checksumLines += "$hash  $($file.Name)"
        $assets += [ordered]@{
            name = $file.Name
            bytes = $file.Length
            sha256 = $hash
            signature = [string]$signature.Status
        }
    }

    $utf8NoBom = New-Object Text.UTF8Encoding($false)
    $checksumPath = Join-Path $OutputDirectory 'SHA256SUMS.txt'
    [IO.File]::WriteAllText($checksumPath, ($checksumLines -join "`n") + "`n", $utf8NoBom)
    $manifest = [ordered]@{
        schemaVersion = 1
        tagName = $TagName
        version = $tagVersion
        commit = (& git -C $codeDirectory rev-parse HEAD | Out-String).Trim()
        createdAt = [DateTimeOffset]::UtcNow.ToString('o')
        assets = $assets
    }
    [IO.File]::WriteAllText((Join-Path $OutputDirectory 'release-assets.json'), ($manifest | ConvertTo-Json -Depth 10) + "`n", $utf8NoBom)
    Write-Host "[release] 完成：$OutputDirectory"
    $checksumLines | ForEach-Object { Write-Host "[release] $_" }
} finally {
    if (Test-Path -LiteralPath $temporarySidecar) {
        Remove-Item -LiteralPath $temporarySidecar -Force
    }
}
