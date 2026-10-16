// Read-only post-install acceptance. Never starts/stops projects or publishes.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
const read=p=>JSON.parse(fs.readFileSync(p,'utf8').replace(/^\uFEFF/,''))
const directory='outputs/acceptance/', before=read(directory+'production-update-before.json'), after=read(directory+'production-update-after.json')
assert.equal(after.health.instanceId,before.health.instanceId)
assert.deepEqual(after.apps,before.apps)
assert.equal(after.oldShellExists,false)
assert.deepEqual(after.backendPIDs,[17532,46388])
assert.equal(after.shells.length,1)
assert.notEqual(after.shells[0].ProcessId,43736)
const install=read(directory+'production-2.0.41-install.json')
assert.equal(install.exitCode,0);assert.equal(install.installedVersion,'2.0.41');assert.equal(install.oldShellStillRunning,true)
const services=[]
for(const url of ['http://127.0.0.1:17654/api/health','http://127.0.0.1:17655/api/health','http://127.0.0.1:17656/','http://127.0.0.1:17654/api/apps/6b2581249d224681/logs']) {
 const response=await fetch(url,{signal:AbortSignal.timeout(5000)})
 assert.equal(response.status,200,url);services.push({url,status:response.status});await response.arrayBuffer()
}
const monitor=read(directory+'production-update-monitor.json')
const failuresDuringFinalInstallAndRestart=monitor.failures.filter(f=>Date.parse(f.at)>=Date.parse(install.startedAt))
assert.deepEqual(failuresDuringFinalInstallAndRestart,[])
const result={passed:true,verifiedAt:new Date().toISOString(),nativeUiObserved:{beforeVersion:'2.0.40',installedPromptVersion:'2.0.41',button:'立即重启',clickedAt:'2026-10-16T06:03:46.299Z',afterVersion:'2.0.41'},installation:install,oldShellPID:43736,newShellPID:after.shells[0].ProcessId,backendPIDs:after.backendPIDs,backendInstanceId:after.health.instanceId,activeProjects:after.apps.filter(a=>a.pid),allProjectRecordsUnchanged:true,services,failuresDuringFinalInstallAndRestart,earlierMonitorFailures:monitor.failures,githubPublished:false,limitations:['Running backend deliberately remains in its original process until normal shutdown.','Native UI restart and local NSIS installation tested; GitHub download was not used.']}
fs.writeFileSync(directory+'production-update-verification.json',JSON.stringify(result,null,2)+'\n')
const sourceFiles=['package.json','package-lock.json','src-tauri/Cargo.toml','src-tauri/Cargo.lock','src-tauri/tauri.conf.json','src-tauri/src/app_update.rs','src-tauri/src/desktop_restart.rs','src-tauri/src/lib.rs','src/stores/appUpdate.ts','src/components/AppUpdateDialog.vue','src/components/GroupSidebar.vue','src/i18n/en.json']
const source={localOnly:true,sourceState:'working-tree-with-uncommitted-changes',baseCommit:execFileSync('git',['rev-parse','HEAD'],{encoding:'utf8'}).trim(),files:sourceFiles.map(file=>({file,sha256:createHash('sha256').update(fs.readFileSync(file)).digest('hex')}))}
fs.writeFileSync('outputs/local-update/2.0.41/local-source.json',JSON.stringify(source,null,2)+'\n')
console.log(JSON.stringify(result,null,2))
