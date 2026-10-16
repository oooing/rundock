import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { reactive } from 'vue'

// Execute the production store with only the native IPC boundary replaced.
const source = readFileSync(new URL('../../src/stores/appUpdate.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source.replace(/^import [^\n]*\n/gm, ''), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function store(invoke) {
  const exports = {}
  class Channel { onmessage = () => {} }
  new Function('exports', 'reactive', 'invoke', 'Channel', compiled)(exports, reactive, invoke, Channel)
  return exports
}
const info = { version: '2.0.14', notes: '', url: '', size: 100 }


function native(calls, overrides={}) { return async (cmd,args) => {
  calls.push({cmd,args})
  if (cmd in overrides) return overrides[cmd](args)
  if (cmd==='app_update_status') return null
  if (cmd==='check_app_update') return info
  if (cmd==='update_install_plan') return {fingerprint:'snapshot-1',projects:[{id:'a',name:'Project A'}]}
} }
test('startup only downloads; installation requires a choice', async()=>{
 const calls=[],s=store(native(calls));await s.prepareStartupUpdate();await s.prepareStartupUpdate()
 assert.equal(s.appUpdate.phase,'ready');assert.deepEqual(calls.map(c=>c.cmd),['app_update_status','check_app_update','download_app_update'])
 await s.installAppUpdate();assert.equal(calls.length,3)
})
test('default next-start choice persists without stopping projects',async()=>{
 const calls=[],s=store(native(calls));s.appUpdate.phase='ready';s.appUpdate.info=info
 await s.scheduleAppUpdate();assert.equal(s.appUpdate.phase,'scheduled');assert.deepEqual(calls.map(c=>c.cmd),['schedule_app_update'])
 s.dismissAppUpdate();s.openAppUpdate();assert.equal(s.appUpdate.dialogOpen,true)
})
test('install requires list then explicit confirmation and sends exact fingerprint',async()=>{
 const calls=[],s=store(native(calls));s.appUpdate.phase='ready';await s.planAppUpdate()
 assert.equal(s.appUpdate.phase,'confirming');assert.equal(s.appUpdate.plan.projects[0].name,'Project A')
 assert.deepEqual(calls.map(c=>c.cmd),['update_install_plan'])
 await s.installAppUpdate();assert.equal(s.appUpdate.phase,'installing');assert.deepEqual(calls.at(-1),{cmd:'install_app_update',args:{fingerprint:'snapshot-1'}})
 await s.installAppUpdate();s.dismissAppUpdate();assert.equal(calls.length,2)
})
test('changed projects invalidate confirmation instead of silently trying again',async()=>{
 const calls=[],s=store(native(calls,{install_app_update:()=>{throw 'Project list changed'}}));s.appUpdate.phase='ready';await s.planAppUpdate();await s.installAppUpdate()
 assert.equal(s.appUpdate.plan,null);assert.equal(s.appUpdate.phase,'ready');assert.match(s.appUpdate.error,/changed/)
 await s.installAppUpdate();assert.equal(calls.filter(c=>c.cmd==='install_app_update').length,1)
})
test('pending schedule survives relaunch without downloading again',async()=>{
 const calls=[],s=store(native(calls,{app_update_status:()=>({version:info.version,phase:'scheduled',projects:[],remaining:[],error:'',failures:[]})}))
 await s.prepareStartupUpdate();assert.equal(s.appUpdate.phase,'scheduled');assert.equal(s.appUpdate.dialogOpen,false);assert.equal(calls.length,1)
 await s.planAppUpdate();assert.equal(s.appUpdate.phase,'confirming')
})
test('partial recovery is visible and retry is explicit',async()=>{
 const calls=[],report={version:info.version,phase:'finished',projects:[{id:'a',name:'A'}],remaining:[{id:'a',name:'A'}],error:'',failures:['A: port occupied']}
 const s=store(native(calls,{app_update_status:()=>report}));await s.prepareStartupUpdate()
 assert.equal(s.appUpdate.phase,'finished');assert.equal(s.appUpdate.dialogOpen,true);assert.deepEqual(s.appUpdate.report.failures,['A: port occupied'])
 await s.retryUpdateRestore();assert.ok(calls.some(c=>c.cmd==='retry_update_restore'))
})
test('external installation uses the same stop-and-restore confirmation',async()=>{
 const calls=[],s=store(native(calls,{check_app_update:()=>({...info,installedLocally:true})}));await s.prepareStartupUpdate()
 assert.equal(s.appUpdate.phase,'installed');assert.ok(!calls.some(c=>c.cmd==='download_app_update'))
 await s.installAppUpdate();assert.ok(!calls.some(c=>c.cmd==='install_app_update'))
 await s.planAppUpdate();assert.equal(s.appUpdate.phase,'confirming')
})
test('download failure stays retryable and never loops',async()=>{
 const calls=[],s=store(native(calls,{download_app_update:()=>{throw 'offline'}}));await s.prepareStartupUpdate();await s.prepareStartupUpdate()
 assert.equal(s.appUpdate.phase,'available');assert.equal(calls.filter(c=>c.cmd==='download_app_update').length,1)
})
test('cancel schedule clears visible state only after native success',async()=>{
 const calls=[],s=store(native(calls));s.appUpdate.phase='scheduled';s.appUpdate.info=info;await s.cancelScheduledUpdate();assert.equal(s.appUpdate.phase,'idle');assert.equal(s.appUpdate.info,null)
})
