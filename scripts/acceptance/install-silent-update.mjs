// Actual installed application acceptance; explicit user authorization required.
// Runs the production installer worker, without any stop/restart API calls.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { spawn, execFileSync } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
const root=process.cwd()
const build=JSON.parse(fs.readFileSync(path.join(root,'outputs/acceptance/latest-silent-update-build.json'),'utf8'))
const evidence=path.join(build.evidence,'live');fs.mkdirSync(evidence,{recursive:true})
const installed=path.join(process.env.LOCALAPPDATA,'RunDock')
const data=path.join(process.env.APPDATA,'launcher-sidecar')
const packageFile=path.join(build.evidence,'packages',`RunDock_${build.version}_x64-setup.exe`)
const digest=p=>createHash('sha256').update(fs.readFileSync(p)).digest('hex')
const json=(name,value)=>fs.writeFileSync(path.join(evidence,name),JSON.stringify(value,null,2))
const pause=ms=>new Promise(r=>setTimeout(r,ms))
async function api(port,route){const r=await fetch(`http://127.0.0.1:${port}${route}`,{signal:AbortSignal.timeout(6000)});assert.ok(r.ok);return r.json()}
async function baseline(){const result={};for(const port of[17654,17655]){result[port]={health:await api(port,'/api/health'),apps:(await api(port,'/api/apps')).map(({id,name,status,pid,runId})=>({id,name,status,pid,runId}))}}return result}
const before=await baseline();json('before.json',before)
const shellProcesses=()=>JSON.parse(execFileSync('powershell.exe',['-NoProfile','-Command',"@(Get-CimInstance Win32_Process -Filter \"Name LIKE 'launcher-platform%'\" | Where-Object { $_.ExecutablePath -like ($env:LOCALAPPDATA + '\\RunDock\\*') } | Select-Object ProcessId,CreationDate | Sort-Object ProcessId) | ConvertTo-Json -Compress"],{encoding:'utf8',windowsHide:true,timeout:15000}))
const shellsBefore=shellProcesses();json('shells-before.json',shellsBefore)
const pending=path.join(data,'desktop-update.json')
if(fs.existsSync(pending)){
 const p=JSON.parse(fs.readFileSync(pending,'utf8'));assert.equal(p.phase,'finished');assert.equal(p.remaining.length,0)
 fs.copyFileSync(pending,path.join(evidence,'desktop-update-before.json'))
}
assert.ok(fs.existsSync(path.join(installed,'uninstall.exe')),'Only the existing NSIS installation is authorized')
const expected=JSON.parse(fs.readFileSync(path.join(build.evidence,'build-result.json'),'utf8'))
assert.equal(expected.exitCode,0)
assert.equal(expected.commit,build.commit,'Wait for the final rebuilt package before installation')
assert.equal(digest(packageFile),expected.artifacts.find(a=>a.name===path.basename(packageFile)).sha256)
const probe=path.join(installed,`rundock-acceptance-${randomUUID()}.exe`)
assert.equal(fs.existsSync(probe),false)
fs.copyFileSync(path.join(root,'src-tauri/target/debug/examples/silent-update-probe.exe'),probe)
const report={version:build.version,evidence,passed:false,githubPublished:false,interruptedProjects:false,samples:[],checks:[]}
let child
try {
 child=spawn(probe,[data,build.version,packageFile,digest(packageFile),String(fs.statSync(packageFile).size)],{cwd:installed,windowsHide:true,stdio:['ignore','pipe','pipe']})
 let out='',err='',launchError;child.stdout.on('data',b=>out+=b);child.stderr.on('data',b=>err+=b);child.on('error',e=>launchError=e)
 const until=Date.now()+210000
 while(child.exitCode===null && !launchError && Date.now()<until){
  const t=Date.now();const now=await baseline()
  for(const port of[17654,17655]){
   assert.equal(now[port].health.instanceId,before[port].health.instanceId,'backend must not restart')
   assert.deepEqual(now[port].apps,before[port].apps,'project state/PID/run must stay unchanged')
  }
  for(const url of['http://127.0.0.1:9100/','http://127.0.0.1:17656/'])assert.equal((await fetch(url,{signal:AbortSignal.timeout(5000)})).status,200)
  report.samples.push({at:new Date().toISOString(),elapsedMs:Date.now()-t,healthy:true})
  await pause(750)
 }
 fs.writeFileSync(path.join(evidence,'installer-worker.log'),out+'\n'+err)
 if(launchError)throw launchError
 assert.equal(child.exitCode,0,err||'Installer worker did not finish')
 const after=await baseline();json('after.json',after);assert.deepEqual(after,before)
 assert.deepEqual(shellProcesses(),shellsBefore,'Installed shell must remain alive')
 const disk=JSON.parse(fs.readFileSync(pending,'utf8'));assert.equal(disk.phase,'staged');assert.equal(disk.version,build.version);assert.deepEqual(disk.remaining,[])
 assert.equal(fs.existsSync(path.join(data,'desktop-update-installer.json')),false)
 assert.equal(fs.existsSync(path.join(installed,'.rundock-install-backup.json')),false)
 // Tauri patches its documented bundle marker when packaging, then restores
 // the unbundled executable. Compare every other byte exactly.
 const normalizeBundle=(file,allowed)=>{
  const bytes=fs.readFileSync(file),found=allowed.flatMap(type=>{
   const marker=Buffer.from(`__TAURI_BUNDLE_TYPE_VAR_${type}`),at=bytes.indexOf(marker)
   if(at<0)return[]
   assert.equal(bytes.indexOf(marker,at+1),-1);return[{type,at}]
  })
  assert.equal(found.length,1)
  Buffer.from('__TAURI_BUNDLE_TYPE_VAR_UNK').copy(bytes,found[0].at)
  return createHash('sha256').update(bytes).digest('hex')
 }
 assert.equal(normalizeBundle(path.join(installed,'launcher-platform.exe'),['NSS','UNK']),normalizeBundle(path.join(build.evidence,'binaries/launcher-platform.exe'),['UNK','MSI']))
 assert.equal(digest(path.join(installed,'launcher-sidecar.exe')),digest(path.join(build.evidence,'binaries/launcher-sidecar.exe')))
 report.checks.push('SHA-256 verified installer executed silently through the production Rust worker', 'Installed shell and backend exactly match the tested build bytes','Original shell/backend/projects remain running; PID/runId unchanged and HTTP remains available','Persistent state is staged, with no pending project restoration or leftover install transaction')
 report.passed=true
}catch(error){report.error=error.stack||String(error);process.exitCode=1}
finally{
 // Exact unique acceptance executable only. Do not alter the application files.
 if(!child || child.exitCode!==null)fs.unlinkSync(probe)
 json('report.json',report);console.log(JSON.stringify(report,null,2))
}
