import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import net from 'node:net';
import test from 'node:test';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..');
const wait=ms=>new Promise(r=>setTimeout(r,ms));
test('development worker preserves restart exit code after HasExited', {skip:process.platform!=='win32',timeout:15000}, async()=>{
 mkdirSync(path.join(root,'.tmp'),{recursive:true});
 const dir=mkdtempSync(path.join(root,'.tmp','dev-exit-'));
 const child=path.join(dir,'worker.ps1');writeFileSync(child,'Start-Sleep -Milliseconds 300\nexit 75\n');
 const quote=s=>"'"+s.replaceAll("'","''")+"'";
 const script=path.join(dir,'verify.ps1');
 writeFileSync(script,`$ErrorActionPreference='Stop'
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile(${quote(path.join(root,'scripts/dev.ps1'))},[ref]$tokens,[ref]$errors)
$fn=$ast.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Start-DevProcess'},$true)
Invoke-Expression $fn.Extent.Text
$script:logDir=${quote(dir)};$script:logFiles=@();$script:children=@()
$worker=Start-DevProcess 'powershell.exe' @('-NoProfile','-File',('"'+${quote(child)}+'"')) ${quote(root)} 'worker'
try {
 if(-not $worker.WaitForExit(5000)){throw 'Worker timeout'}
 if(-not $worker.HasExited -or $worker.ExitCode -ne 75){throw 'Restart exit code lost'}
 if($script:children.Count -ne 1){throw 'Worker ownership lost'}
 Write-Output 'restart-code-preserved'
} finally {if(-not $worker.HasExited){$worker.Kill()}}
`);
 const result=await new Promise((resolve,reject)=>{
  const process=spawn('powershell.exe',['-NoProfile','-ExecutionPolicy','Bypass','-File',script],{windowsHide:true,stdio:['ignore','pipe','pipe']});
  let output='';process.stdout.on('data',b=>output+=b);process.stderr.on('data',b=>output+=b);
  process.on('error',reject);process.on('close',code=>resolve({code,output}));
 });
 assert.equal(result.code,0,result.output);assert.match(result.output,/restart-code-preserved/);
});
test('forced development supervisor exit reclaims its hidden child', {skip:process.platform!=='win32',timeout:20000}, async()=>{
 mkdirSync(path.join(root,'.tmp'),{recursive:true});
 const dir=mkdtempSync(path.join(root,'.tmp','dev-job-'));
 const reservation=net.createServer();await new Promise(r=>reservation.listen(0,'127.0.0.1',r));const port=reservation.address().port;await new Promise(r=>reservation.close(r));
 const listener=path.join(dir,'listener.cjs');writeFileSync(listener,`require('http').createServer((q,s)=>s.end('ready')).listen(${port},'127.0.0.1')`);
 const quote=s=>"'"+s.replaceAll("'","''")+"'";
 const script=path.join(dir,'guard.ps1');
 writeFileSync(script,`$ErrorActionPreference='Stop'\nAdd-Type -Path ${quote(path.join(root,'scripts/dev-job.cs'))}\n[RunDockDevJob]::Attach()\n$p=Start-Process -FilePath ${quote(process.execPath)} -ArgumentList ${quote('"'+listener+'"')} -WindowStyle Hidden -PassThru\nWrite-Output $p.Id\nwhile($true){Start-Sleep -Milliseconds 200}\n`);
 const supervisor=spawn('powershell.exe',['-NoProfile','-ExecutionPolicy','Bypass','-File',script],{windowsHide:true,stdio:['ignore','pipe','pipe']});
 let output='';supervisor.stdout.on('data',b=>output+=b);supervisor.stderr.on('data',b=>output+=b);
 async function live(){try{return (await fetch(`http://127.0.0.1:${port}`,{signal:AbortSignal.timeout(500)})).ok}catch{return false}}
 try{
  let ready=false;for(let i=0;i<60;i++){if(await live()){ready=true;break}await wait(100)}assert.ok(ready,output);
  supervisor.kill(); // No finally handlers: equivalent to forcibly closing the startup process.
  let gone=false;for(let i=0;i<40;i++){if(!await live()){gone=true;break}await wait(100)}assert.ok(gone,'child listener survived supervisor termination');
 }finally{if(supervisor.exitCode===null)supervisor.kill();const childPID=Number(output.trim().split(/\s+/)[0]);if(Number.isInteger(childPID)&&childPID>4){try{process.kill(childPID)}catch{}}}
});
