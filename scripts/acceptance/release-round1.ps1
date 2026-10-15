param([string]$Backend='http://127.0.0.1:17655')
$ErrorActionPreference='Stop'
$root=Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$data=Join-Path $root ('outputs/acceptance/release-round1-'+[Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $data -Force | Out-Null
function Request($method,$url,$body=$null) {
 if ($method -eq 'POST' -and $url -match '/releases(?:\?|$)') {throw 'Formal publication is forbidden in this acceptance'}
 $options=@{Method=$method;Uri=$url;TimeoutSec=240}
 if($null -ne $body){$options.ContentType='application/json';$options.Body=ConvertTo-Json -InputObject $body -Depth 30 -Compress}
 $result=Invoke-RestMethod @options
 if($result -is [string]) {$result=$result|ConvertFrom-Json -AsHashtable}
 return $result
}
$priorData=$env:LAUNCHER_DATA_DIR;$priorDev=$env:LAUNCHER_DEV;$priorSupervisor=$env:LAUNCHER_SUPERVISOR_PID
$probe=$null;$report=@();$drafts=@();$liveHealth=Request Get "$Backend/api/health"
function Start-Probe {
 $port=Join-Path $data 'sidecar.port'
 if(Test-Path $port) {Remove-Item -LiteralPath $port}
 $script:probe=Start-Process -FilePath (Join-Path $root 'sidecar/.tmp/launcher-sidecar-round1.exe') -ArgumentList '-addr','127.0.0.1:0' -WorkingDirectory $root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $data ('out-'+[Guid]::NewGuid().ToString('N')+'.log')) -RedirectStandardError (Join-Path $data ('err-'+[Guid]::NewGuid().ToString('N')+'.log'))
 $deadline=[DateTime]::UtcNow.AddSeconds(20)
 while(!(Test-Path $port)) {if($probe.HasExited -or [DateTime]::UtcNow -gt $deadline){throw 'Isolated backend failed to start'};Start-Sleep -Milliseconds 100}
 $script:base='http://127.0.0.1:'+(Get-Content $port -Raw).Trim()
}
try {
 $env:LAUNCHER_DATA_DIR=$data;$env:LAUNCHER_DEV='1';$env:LAUNCHER_SUPERVISOR_PID=$null
 Start-Probe
 foreach($item in @(@{id='2f1a038659086733';name='RunDock';targets=@('local-windows')},@{id='d87363129d2c76a9';name='ingLocalPlay';targets=@('desktop-local','web-server-local','android-local')})) {
  $liveApp=Request Get "$Backend/api/apps/$($item.id)"
  $historyBefore=Request Get "$Backend/api/apps/$($item.id)/releases?limit=30"
  $app=Request Post "$base/api/apps" @{name=$item.name+' acceptance';cwd=$liveApp.cwd;entryScript=$liveApp.entryScript;adapterType='batch'}
  $watch=[Diagnostics.Stopwatch]::StartNew()
  $pf=Request Post "$base/api/apps/$($app.id)/release/preflight?remote=false"
  $preflightSeconds=$watch.Elapsed.TotalSeconds
  $cfg=Request Get "$base/api/apps/$($app.id)/release-config"
  $targets=@($cfg.targets|Where-Object {$_.id -in $item.targets})
  if($targets.Count -ne $item.targets.Count) {throw 'Missing configured target'}
  $selected=@($pf.classifications|Where-Object {$_.selectedDefault}|ForEach-Object {$_.path})
  $decisions=@($pf.classifications|Where-Object {-not $_.selectedDefault}|ForEach-Object {@{path=$_.path;decision='exclude';contentFingerprint=$_.contentFingerprint;reason='Acceptance only: leave unselected files untouched'}})
  $versions=@($targets|ForEach-Object {$_.versionGroup}|Select-Object -Unique|ForEach-Object {@{versionGroupId=$_;targetVersion=$pf.suggestedVersions.$_}})
  $req=@{intent='formal';statusFingerprint=$pf.statusFingerprint;selectedPaths=$selected;manualDecisions=$decisions;selectedTargets=@($targets|ForEach-Object {@{targetId=$_.id;build=$true;package=$false;publish=$true;deploy=$false}});versions=$versions;versionMode='manual';createTag=$true;pushRemote=$true;buildMode='local';sensitiveExceptions=@()}
  $draft=@{id=[Guid]::NewGuid().ToString();revision=0;status='checking';candidateId='';runId='';message='';draft=@{request=$req;commitMessage='Acceptance only';releaseNotes='Acceptance only; do not publish';syncPolicy='auto'}}
  $saved=Request Put "$base/api/apps/$($app.id)/release/preparations" $draft
  $watch.Restart()
  $candidate=Request Post "$base/api/apps/$($app.id)/release/candidate" $req
  $reviewSeconds=$null
  if($item.name -eq 'ingLocalPlay' -and @($candidate.sensitiveFindings).Count -eq 1) {
   $finding=$candidate.sensitiveFindings[0]
   # This exact test fixture was inspected and the user has already authorized
   # allowing it. Never extend that authorization to another file or finding.
   if($finding.path -eq 'code/backend/tests/test_user_management.py' -and $finding.line -eq 63 -and $finding.kind -eq 'secret-assignment') {
    $reviewWatch=[Diagnostics.Stopwatch]::StartNew()
    $candidate=Request Post "$base/api/apps/$($app.id)/release/candidate/review" @{candidateId=$candidate.id;sensitiveExceptions=@(@{path=$finding.path;reason='User-approved test password fixture';contentFingerprint=$finding.contentFingerprint;findingFingerprint=$finding.fingerprint})}
    $reviewSeconds=$reviewWatch.Elapsed.TotalSeconds
   }
  }
  if($candidate.status -eq 'pending') {$candidate=Request Post "$base/api/apps/$($app.id)/release/candidate/check" @{candidateId=$candidate.id}}
  $checkSeconds=$watch.Elapsed.TotalSeconds
  $saved.candidateId=$candidate.id;$saved.status=if($candidate.accepted){'ready'}else{'blocked'}
  $saved=Request Put "$base/api/apps/$($app.id)/release/preparations" $saved
  $drafts+=@{app=$app.id;id=$saved.id;status=$saved.status}
  $after=Request Post "$base/api/apps/$($app.id)/release/preflight?remote=false"
  $historyAfter=Request Get "$Backend/api/apps/$($item.id)/releases?limit=30"
  if($after.statusFingerprint -ne $pf.statusFingerprint -or $after.headSha -ne $pf.headSha){throw 'Source changed during acceptance'}
  if((ConvertTo-Json -InputObject $historyBefore -Depth 30 -Compress) -ne (ConvertTo-Json -InputObject $historyAfter -Depth 30 -Compress)){throw 'Live release history changed'}
  $report+=[ordered]@{project=$item.name;preflightSeconds=$preflightSeconds;checkSeconds=$checkSeconds;reviewSeconds=$reviewSeconds;status=$candidate.status;accepted=$candidate.accepted;selectedFiles=$selected.Count;checks=@($candidate.checkResults|Select-Object id,name,status,reason);sensitiveFindings=@($candidate.sensitiveFindings|Select-Object path,line,kind,reason);blockingDependencies=@($candidate.dependencyFindings|Where-Object blocked|Select-Object path,reason);sourceUnchanged=$true;releaseHistoryUnchanged=$true;published=$false}
  # Cancel explicitly to reclaim the isolated candidate, without touching source.
  $null=Request Post "$base/api/apps/$($app.id)/release/candidate/cancel" @{candidateId=$candidate.id}
 }
 Stop-Process -Id $probe.Id; $probe.WaitForExit(); Start-Probe
 foreach($draft in $drafts) {
  $rows=@(Request Get "$base/api/apps/$($draft.app)/release/preparations")
  $restored=$rows|Where-Object {$_.id -eq $draft.id}
  if(!$restored -or $restored.status -ne 'interrupted' -or $restored.runId){throw 'Restart recovery failed'}
 }
 $result=[ordered]@{measuredAt=[DateTime]::UtcNow.ToString('o');projects=$report;restartRecovery=$true;liveInstanceUnchanged=((Request Get "$Backend/api/health").instanceId -eq $liveHealth.instanceId);published=$false}
 $result|ConvertTo-Json -Depth 12|Set-Content -LiteralPath (Join-Path $data 'report.json') -Encoding utf8
 Write-Output (Join-Path $data 'report.json')
 $result|ConvertTo-Json -Depth 12
} finally {
 if($probe -and !$probe.HasExited){Stop-Process -Id $probe.Id}
 $env:LAUNCHER_DATA_DIR=$priorData;$env:LAUNCHER_DEV=$priorDev;$env:LAUNCHER_SUPERVISOR_PID=$priorSupervisor
}
