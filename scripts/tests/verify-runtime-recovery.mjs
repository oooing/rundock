// Real Windows process/API checks. All apps, processes and data are disposable;
// this never starts/stops a user's project or launches an external browser.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdirSync, writeFileSync, readFileSync, existsSync } from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const dir = path.join(root, '.tmp', `runtime-check-${Date.now()}`)
const project = path.join(dir, 'project'), data = path.join(dir, 'data')
mkdirSync(project, {recursive:true}); mkdirSync(data, {recursive:true})
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
const exited = p => p.exitCode !== null || p.signalCode !== null
async function until(fn, timeout=35000) {
  const end=Date.now()+timeout
  while(Date.now()<end) { const result=await fn(); if(result)return result; await delay(250) }
  throw new Error('Timed out waiting for runtime state')
}
async function freePort() {
  const server=net.createServer(); await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
  const port=server.address().port; await new Promise(resolve=>server.close(resolve)); return port
}
const servicePort=await freePort(), apiPort=await freePort()
const base=`http://127.0.0.1:${apiPort}`
async function request(route, body) {
  const res=await fetch(base+route,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)})
  const result=await res.json()
  if(!res.ok)throw Object.assign(new Error(JSON.stringify(result)),{status:res.status})
  return result
}
async function reachable() {
  try{return (await fetch(`http://127.0.0.1:${servicePort}`,{signal:AbortSignal.timeout(1000)})).ok}catch{return false}
}
let backend, survivor, stranger, app
function child(file,args,env={}) {
  const p=spawn(file,args,{windowsHide:true,env:{...process.env,...env},stdio:['ignore','ignore','pipe']})
  p.stderr.on('data',chunk=>{ p.lastError=(p.lastError||'')+chunk.toString(); if(p.lastError.length>4000)p.lastError=p.lastError.slice(-4000) })
  p.on('exit',code=>{if(code && p.lastError)console.error(p.lastError)})
  return p
}
async function boot() {
  backend=child(path.join(root,'sidecar/.tmp/runtime-recovery-validation.exe'),['-port',`${apiPort}`],{LAUNCHER_DATA_DIR:data})
  backend.stderr.on('data',()=>{})
  await until(async()=>{try{return (await request('/api/health')).status==='ok'}catch{return false}})
}
async function shutdown() {
  await request('/api/desktop/shutdown',{})
  await until(()=>exited(backend))
}
async function recheck() { return request(`/api/apps/${app.id}/runtime-check`,{}) }
const serverFile=path.join(project,'server.cjs'), script=path.join(project,'start.cmd'), marker=path.join(project,'starts.txt')
writeFileSync(serverFile,String.raw`require('fs').appendFileSync(__dirname+'/starts.txt','start\n'); require('http').createServer((q,s)=>s.end('fixture')).listen(${servicePort},'127.0.0.1',()=>console.log('http://localhost:${servicePort}'));`)
writeFileSync(script,`@echo off\r\nrem rundock:ready http://127.0.0.1:${servicePort}\r\nrem rundock:open http://127.0.0.1:${servicePort}\r\n"${process.execPath}" "%~dp0server.cjs"\r\n`.replaceAll('\\r\\n','\r\n'))
const starts=()=>existsSync(marker)?readFileSync(marker,'utf8').trim().split('\n').length:0
try {
  await boot()
  const candidate=await request('/api/import',{scriptPath:script})
  const {entryScript,adapterType,cmd,args,env,scriptHash}=candidate
  app=await request('/api/apps',{entryScript,adapterType,cmd,args,env,scriptHash,cwd:project,name:'Runtime recovery fixture',portHints:[servicePort],healthUrl:`http://127.0.0.1:${servicePort}`})
  survivor=child(process.execPath,[serverFile]); survivor.stderr.on('data',()=>{})
  await until(reachable)
  let state=await recheck()
  assert.equal(state.status,'running'); assert.equal(state.runtimeCheck.state,'running'); assert.equal(state.runId,'')
  const identity=survivor.pid
  await shutdown(); assert.ok(await reachable())
  await boot(); state=await recheck()
  assert.equal(state.status,'running'); assert.equal(state.pid,identity)
  for(const op of ['start','restart','stop'])await assert.rejects(request(`/api/apps/${app.id}/${op}`,{}))
  assert.equal(starts(),1); assert.ok(await reachable())
  console.log('PASS: backend restart restores surviving service status; start/stop/restart do not duplicate or kill it')

  survivor.kill(); await until(()=>exited(survivor)); await until(async()=>!await reachable())
  state=await recheck(); assert.equal(state.status,'stopped'); assert.equal(state.runtimeCheck.state,'clear')
  stranger=child(process.execPath,['-e',`require('http').createServer((q,s)=>s.end('other')).listen(${servicePort},'127.0.0.1')`]); stranger.stderr.on('data',()=>{})
  await until(reachable)
  // Do not recheck the card first: the startup guard itself must catch the new owner.
  await assert.rejects(request(`/api/apps/${app.id}/start`,{}))
  state=await request(`/api/apps/${app.id}`); assert.equal(state.runtimeCheck.state,'conflict')
  assert.equal(starts(),1); assert.ok(await reachable())
  console.log('PASS: occupied port detected before script execution; unrelated service remains alive')
  stranger.kill(); await until(()=>exited(stranger)); await until(async()=>!await reachable())
  await recheck()

  const attempts=await Promise.allSettled([request(`/api/apps/${app.id}/start`,{}),request(`/api/apps/${app.id}/start`,{})])
  assert.equal(attempts.filter(x=>x.status==='fulfilled').length,1)
  state=await until(async()=>{const s=await request(`/api/apps/${app.id}`);return s.status==='running'&&s.runId&&s})
  assert.equal(starts(),2)
  const ownedPID=state.pid, ownedRun=state.runId
  state=await request(`/api/apps/${app.id}`); assert.equal(state.pid,ownedPID); assert.equal(state.runId,ownedRun)
  await request(`/api/apps/${app.id}/restart`,{})
  await until(async()=>{const s=await request(`/api/apps/${app.id}`);return s.status==='running'&&s.runId!==ownedRun})
  assert.equal(starts(),3)
  await request(`/api/apps/${app.id}/stop`,{})
  state=await request(`/api/apps/${app.id}`); assert.equal(state.status,'stopped'); assert.ok(!await reachable())
  console.log('PASS: concurrent starts create one process; managed restart/stop and UI reconnect retain correct state')

  await request(`/api/apps/${app.id}/start`,{})
  await until(async()=>{const s=await request(`/api/apps/${app.id}`);return s.status==='running'})
  backend.kill(); await until(()=>exited(backend)); await until(async()=>!await reachable())
  await boot(); state=await recheck(); assert.equal(state.status,'stopped')
  console.log('PASS: backend crash terminates its owned process; reboot does not invent a live status or auto-start it')
  console.log(`Evidence: ${dir}`)
} finally {
  if(backend && !exited(backend)) { try{await shutdown()}catch{backend.kill()} }
  if(survivor && !exited(survivor))survivor.kill()
  if(stranger && !exited(stranger))stranger.kill()
}
