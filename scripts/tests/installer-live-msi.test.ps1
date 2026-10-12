param([string]$Go='go', [string]$WixTools="$env:LOCALAPPDATA\tauri\WixTools314")
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$repo=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$stage=Join-Path $repo ('.tmp/installer-live-msi-'+[Guid]::NewGuid().ToString('N'))
$installed=Join-Path $stage 'installed 中文'
New-Item -ItemType Directory -Path $stage -Force | Out-Null
$upgrade=[Guid]::NewGuid().ToString()
$shellComponent=[Guid]::NewGuid().ToString()
$backendComponent=[Guid]::NewGuid().ToString()
$processes=@()
$installedProduct=$false
function Check([bool]$Condition,[string]$Message) { if(-not $Condition){throw $Message} }
function Build-Installer([string]$Version,[switch]$Fail) {
 $source=Join-Path $stage "$Version.wxs"
 $exe=Join-Path $stage "owner-$Version.exe"
 & $Go -C (Join-Path $repo 'sidecar') build -ldflags "-X main.BuildVersion=$Version" -o $exe (Join-Path $PSScriptRoot 'fixtures/installer-owner.go')
 Check ($LASTEXITCODE -eq 0) 'Fixture build failed'
 $liveRef=if($Version -ne '1.0.0'){'<ComponentGroupRef Id="RunDockLiveInstall" />'}else{''}
 $fault=if($Fail){'<CustomAction Id="IntentionalFailure" Error="Isolated rollback test" /><InstallExecuteSequence><Custom Action="IntentionalFailure" Before="InstallFiles">1</Custom></InstallExecuteSequence>'}else{''}
 @"
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
<Product Id="*" Name="RunDock isolated live test $upgrade" Language="1033" Version="$Version" Manufacturer="RunDock test" UpgradeCode="$upgrade">
<Package InstallerVersion="450" Compressed="yes" InstallScope="perUser" InstallPrivileges="limited" />
<MajorUpgrade Schedule="afterInstallInitialize" AllowDowngrades="yes" />
<Media Id="1" Cabinet="fixture.cab" EmbedCab="yes" />
<Directory Id="TARGETDIR" Name="SourceDir"><Directory Id="LocalAppDataFolder"><Directory Id="INSTALLDIR" Name="RunDockTest" /></Directory></Directory>
<DirectoryRef Id="INSTALLDIR"><Component Id="Shell" Guid="$shellComponent" Win64="yes"><File Id="ShellFile" Source="$exe" Name="launcher-platform.exe" /><RegistryValue Root="HKCU" Key="Software\RunDockInstallerTests\$upgrade" Name="Shell" Value="1" Type="integer" KeyPath="yes" /><RemoveFolder Id="RemoveTestFolder" On="uninstall" /></Component><Component Id="Backend" Guid="$backendComponent" Win64="yes"><File Id="BackendFile" Source="$exe" Name="launcher-sidecar.exe" /><RegistryValue Root="HKCU" Key="Software\RunDockInstallerTests\$upgrade" Name="Backend" Value="1" Type="integer" KeyPath="yes" /></Component></DirectoryRef>
<Feature Id="Main" Level="1"><ComponentRef Id="Shell" /><ComponentRef Id="Backend" />$liveRef</Feature>
$fault
</Product></Wix>
"@ | Set-Content -LiteralPath $source -Encoding UTF8
 $args=@('-nologo','-arch','x64','-out',"$stage\",$source)
 if($Version -ne '1.0.0'){$args += Join-Path $repo 'src-tauri/windows/live-install.wxs'}
 & (Join-Path $WixTools 'candle.exe') @args
 Check ($LASTEXITCODE -eq 0) 'WiX compile failed'
 $objects=@((Join-Path $stage "$Version.wixobj"))
 if($Version -ne '1.0.0'){$objects += Join-Path $stage 'live-install.wixobj'}
 & (Join-Path $WixTools 'light.exe') -nologo -out (Join-Path $stage "$Version.msi") @objects
 Check ($LASTEXITCODE -eq 0) 'WiX linking failed'
}
function Install([string]$Version,[switch]$Fail) {
 $package=Join-Path $stage "$Version.msi"
 $log=Join-Path $stage "$Version.log"
 $args='/i "'+$package+'" /qn /norestart INSTALLDIR="'+$installed+'" /l*v "'+$log+'"'
 $p=Start-Process -FilePath "$env:SystemRoot\System32\msiexec.exe" -ArgumentList $args -WindowStyle Hidden -Wait -PassThru
 if($Fail){Check ($p.ExitCode -ne 0) 'Injected MSI error was not reported'}else{Check ($p.ExitCode -eq 0) "MSI $Version failed (code $($p.ExitCode)); see $log"}
}
function Start-Owner([string]$Name) {
 $ready=Join-Path $installed "$Name.ready"
 $p=Start-Process -FilePath (Join-Path $installed "$Name.exe") -ArgumentList ('-ready "'+$ready+'"') -WindowStyle Hidden -PassThru
 for($i=0;$i -lt 60 -and (-not (Test-Path $ready) -or -not (Test-Path "$ready.project"));$i++){Start-Sleep -Milliseconds 100}
 Check (Test-Path "$ready.project") 'Owned project did not start'
 return $p
}
try {
 Build-Installer '1.0.0'
 Build-Installer '2.0.0'
 Build-Installer '3.0.0' -Fail
 Install '1.0.0'; $installedProduct=$true
 $backend=Start-Owner 'launcher-sidecar';$processes+=$backend
 $shell=Start-Owner 'launcher-platform';$processes+=$shell
 $ready=Join-Path $installed 'launcher-sidecar.ready'
 $projectPort=(Get-Content "$ready.project" -Raw).Trim()
 $projectPID=[int](Get-Content "$ready.project.pid" -Raw)
 $data=Join-Path $installed 'project-data.json';'preserve projects'|Set-Content $data
 $hash=(Get-FileHash $data).Hash
 Install '2.0.0'
 Check (-not $backend.HasExited -and -not $shell.HasExited) 'MSI stopped a running owner'
 Check ((Invoke-RestMethod "http://$projectPort/api/health" -Proxy $null -TimeoutSec 3).version -eq '1.0.0') 'Original project did not keep running'
 Check ($null -ne (Get-Process -Id $projectPID -ErrorAction SilentlyContinue)) 'Project PID changed'
 Check ((Get-FileHash (Join-Path $installed 'launcher-sidecar.exe')).Hash -eq (Get-FileHash (Join-Path $stage 'owner-2.0.0.exe')).Hash) 'New version not installed'
 Check (-not (Test-Path "$ready.graceful")) 'MSI requested shutdown'
 Check ((Get-FileHash $data).Hash -eq $hash) 'MSI changed project data'
 Write-Output 'PASS: real MSI major upgrade replaces binaries while shell, backend, original Job-owned project PID and data remain intact.'
 Install '3.0.0' -Fail
 Check (-not $backend.HasExited -and -not $shell.HasExited) 'Failed MSI stopped running owners'
 Check ((Invoke-RestMethod "http://$projectPort/api/health" -Proxy $null -TimeoutSec 3).status -eq 'ok') 'Failed MSI stopped its managed project'
 Check ((Get-FileHash (Join-Path $installed 'launcher-sidecar.exe')).Hash -eq (Get-FileHash (Join-Path $stage 'owner-2.0.0.exe')).Hash) 'Failed MSI did not restore installed binaries'
 Check (-not (Test-Path (Join-Path $installed '.rundock-install-backup.json'))) 'MSI left a recovery journal after rollback'
 Write-Output 'PASS: MSI failure rolls files back without stopping existing projects.'
} finally {
 foreach($p in $processes){if(-not $p.HasExited){Stop-Process -Id $p.Id -ErrorAction SilentlyContinue};$p.Dispose()}
 # Only this test's distinct product is removed; never touch installed RunDock.
 if($installedProduct){$package=Join-Path $stage '2.0.0.msi';Start-Process -FilePath "$env:SystemRoot\System32\msiexec.exe" -ArgumentList ('/x "'+$package+'" /qn /norestart') -WindowStyle Hidden -Wait | Out-Null}
}
