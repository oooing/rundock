[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Version,
    [Parameter(Mandatory)][string]$Commit,
    [string]$Directory = '.tmp/release-assets',
    [ValidateSet('Unsigned', 'Authenticode')][string]$SignaturePolicy = 'Unsigned',
    [string]$CertificateThumbprint
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($Version -notmatch '^\d+\.\d+\.\d+$' -or $Commit -notmatch '^[a-f0-9]{40,64}$') {
    throw 'Expected a frozen version and commit.'
}
$root = (Resolve-Path -LiteralPath $Directory).Path
$manifest = Get-Content -LiteralPath (Join-Path $root 'release-assets.json') -Encoding UTF8 -Raw | ConvertFrom-Json
if ($manifest.version -cne $Version -or $manifest.commit -cne $Commit) { throw 'Build manifest does not match the frozen source.' }
$expected = @("RunDock_${Version}_x64-setup.exe", "RunDock_${Version}_x64_en-US.msi")
$checksums = @()
foreach ($name in $expected) {
    $path = Join-Path $root $name
    $file = Get-Item -LiteralPath $path
    if ($file.Length -eq 0 -or ($file.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw "Invalid installer: $name" }
    $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
    $entry = @($manifest.assets | Where-Object { $_.name -ceq $name })
    if ($entry.Count -ne 1 -or $entry[0].sha256 -cne $hash -or $entry[0].bytes -ne $file.Length) { throw "Installer hash mismatch: $name" }
    $signature = Get-AuthenticodeSignature -LiteralPath $path
    if ($SignaturePolicy -eq 'Authenticode') {
        if (-not $CertificateThumbprint -or $signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ine $CertificateThumbprint) { throw "Signer does not match the configured certificate: $name" }
    } elseif ($signature.Status -ne 'NotSigned') {
        # Existing RunDock CI ships unsigned installers. Changing this policy
        # requires explicitly pinning the publisher certificate above.
        throw "Expected the existing unsigned policy; review the new signing identity: $name"
    }
    $checksums += "$hash  $name"
}
$actual = (Get-Content -LiteralPath (Join-Path $root 'SHA256SUMS.txt') -Encoding UTF8 | Where-Object { $_.Trim() } | Sort-Object) -join "`n"
if ($actual -cne (($checksums | Sort-Object) -join "`n")) { throw 'SHA256SUMS does not match the complete installer set.' }
$exeVersion = (Get-Item -LiteralPath (Join-Path $root $expected[0])).VersionInfo.ProductVersion
if ($exeVersion -notmatch ('^' + [regex]::Escape($Version) + '(\.0)?$')) { throw "Installer embedded version differs: $exeVersion" }
$installer = New-Object -ComObject WindowsInstaller.Installer
try {
    $database = $installer.OpenDatabase((Join-Path $root $expected[1]), 0)
    $view = $database.OpenView("SELECT Value FROM Property WHERE Property = 'ProductVersion'")
    $view.Execute(); $record = $view.Fetch()
    if ($record.StringData(1) -cne $Version) { throw 'MSI embedded ProductVersion differs.' }
    if ($database.SummaryInformation(0).Property(7) -notmatch '^x64;') { throw 'MSI is not x64.' }
} finally {
    foreach ($name in @('record', 'view', 'database', 'installer')) {
        $value = Get-Variable -Name $name -ValueOnly -ErrorAction SilentlyContinue
        if ($null -ne $value -and [Runtime.InteropServices.Marshal]::IsComObject($value)) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($value) }
    }
}
Write-Host "Verified RunDock $Version, x64, source $Commit, signature policy: $SignaturePolicy"
