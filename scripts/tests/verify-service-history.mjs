// Real isolated backend + two HTTP services; never starts a user's project.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import net from 'node:net'
const root = process.cwd(), dir = mkdtempSync(path.join(root, '.tmp/service-history-'))
const delay = ms => new Promise(r => setTimeout(r, ms))
async function port() { const s=net.createServer(); await new Promise(r=>s.listen(0,'127.0.0.1',r)); const p=s.address().port; await new Promise(r=>s.close(r)); return p }
async function until(fn) { for(let i=0;i<120;i++){const v=await fn();if(v)return v;await delay(250)} throw Error('readiness timeout') }
const apiPort=await port(), front=await port(), back=await port(), base=`http://127.0.0.1:${apiPort}`
async function request(route, body) { const r=await fetch(base+route,{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined}); const data=await r.json(); assert.ok(r.ok,JSON.stringify(data)); return data }
const backend=spawn(path.join(root,'sidecar/.tmp/recovery-validation-backend.exe'),['-port',`${apiPort}`],{windowsHide:true,env:{...process.env,LAUNCHER_DATA_DIR:path.join(dir,'data')},stdio:'ignore'})
let app
try {
 await until(async()=>{try{return (await fetch(base+'/api/health')).ok}catch{return false}})
 const cwd=path.join(dir,'project');mkdirSync(cwd)
 const script=path.join(cwd,'start.bat')
 writeFileSync(script,`@echo off\r\n"${process.execPath}" "%~dp0server.cjs"\r\n`)
 writeFileSync(path.join(cwd,'server.cjs'),`if(require('fs').existsSync(__dirname+'/fail')){console.error('fixture restart failure');process.exit(1)};for(const p of [${front},${back}])require('http').createServer((q,s)=>s.end('ok')).listen(p,'127.0.0.1',()=>console.log('Listening http://localhost:'+p));`)
 const candidate=await request('/api/import',{scriptPath:script})
 const {entryScript,adapterType,cmd,args,env,scriptHash}=candidate
 app=await request('/api/apps',{entryScript,adapterType,cmd,args,env,scriptHash,name:'Two-service test',cwd,portHints:[front,back]})
 await request(`/api/apps/${app.id}/start`,{})
 await until(async()=>{const a=await request(`/api/apps/${app.id}`);return a.services.length===2&&a.status==='running'})
 await request(`/api/apps/${app.id}/restart`,{})
 await until(async()=>{const a=await request(`/api/apps/${app.id}`);return a.services.length===2&&a.status==='running'})
 await request(`/api/apps/${app.id}/stop`,{})
 let state=await request(`/api/apps/${app.id}`)
 assert.equal(state.services.length,0);assert.equal(state.knownServices.length,2)
 writeFileSync(path.join(cwd,'fail'),'1')
 await request(`/api/apps/${app.id}/start`,{})
 state=await until(async()=>{const a=await request(`/api/apps/${app.id}`);return a.status==='failed'&&a})
 assert.equal(state.services.length,0);assert.equal(state.knownServices.length,2)
 assert.deepEqual(state.knownServices.map(s=>s.port).sort(),[front,back].sort())
 const logs=await request(`/api/apps/${app.id}/logs?limit=100`)
 assert.ok(logs.logs.some(l=>l.text.includes('fixture restart failure')))
 assert.ok(!logs.logs.some(l=>l.text.includes('Listening http')), 'failure logs must belong to the latest failed run')
 console.log('PASS: two services discovered, real restart succeeds, stop/failure retain both addresses, latest failure logs accessible.')
} finally {
 if(app)try{await request(`/api/apps/${app.id}/stop`,{})}catch{}
 try{await request('/api/desktop/shutdown',{})}catch{}
 for(let i=0;i<30&&backend.exitCode===null;i++)await delay(100)
 if(backend.exitCode===null)backend.kill()
}
