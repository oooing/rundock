// Isolated Windows process handoff with a real sidecar and disposable project.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import net from 'node:net'
const root=process.cwd(), dir=mkdtempSync(path.join(root,'.tmp','desktop-restart-'))
const canonical=path.join(dir,'launcher-platform.exe'), old=path.join(dir,'launcher-platform.rundock-running-old.exe'), marker=path.join(dir,'reopened.txt')
copyFileSync(path.join(root,'.tmp/desktop-restart-host.exe'),canonical);copyFileSync(canonical,old)
const delay=ms=>new Promise(r=>setTimeout(r,ms))
async function until(fn){for(let i=0;i<160;i++){if(await fn())return;await delay(250)}throw Error('Timed out')}
async function port(){const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p}
const apiPort=await port(), projectPort=await port(), base=`http://127.0.0.1:${apiPort}`
async function request(url,body){const res=await fetch(base+url,{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(10000)});assert.ok(res.ok,`${url}: ${res.status} ${res.ok?'':await res.text()}`);return res.json()}
async function healthy(url){try{return (await fetch(url,{signal:AbortSignal.timeout(1500)})).ok}catch{return false}}
const env={...process.env,RESTART_FIXTURE_MARKER:marker}
const rejected=spawn(old,['reject-version'],{env,windowsHide:true,stdio:'pipe'})
let rejection='';rejected.stdout.on('data',b=>rejection+=b)
await until(()=>rejected.exitCode!==null);assert.equal(rejected.exitCode,0);assert.match(rejection,/old-shell-still-running/);assert.ok(!existsSync(marker))
const host=spawn(old,['keep',`${apiPort}`,path.join(root,'sidecar/.tmp/desktop-exit-backend.exe'),path.join(dir,'data'),path.join(dir,'backend.log')],{env,windowsHide:true,stdio:['pipe','pipe','pipe']})
let output='',errors='',backendPID,newPID
host.stdout.on('data',b=>output+=b);host.stderr.on('data',b=>errors+=b)
try {
 await until(()=>healthy(base+'/api/health'));backendPID=Number(output.trim());assert.ok(backendPID>0)
 const cwd=path.join(dir,'project');mkdirSync(cwd)
 writeFileSync(path.join(cwd,'server.cjs'),`require('http').createServer((q,s)=>s.end('alive')).listen(${projectPort},'127.0.0.1');console.log('restart fixture ready');`)
 writeFileSync(path.join(cwd,'start.bat'),`@echo off\r\n"${process.execPath}" "%~dp0server.cjs"\r\n`)
 const imported=await request('/api/import',{scriptPath:path.join(cwd,'start.bat')})
 const {entryScript,adapterType,cmd,args,env:projectEnv,scriptHash}=imported
 const app=await request('/api/apps',{name:'Restart fixture',cwd,entryScript,adapterType,cmd,args,env:projectEnv,scriptHash,portHints:[projectPort]})
 await request(`/api/apps/${app.id}/start`,{})
 await until(()=>healthy(`http://127.0.0.1:${projectPort}`))
 await until(async()=>['running','degraded'].includes((await request(`/api/apps/${app.id}`)).status))
 const before=await request(`/api/apps/${app.id}`), health=await request('/api/health')
 host.stdin.end('restart\n')
 await until(()=>existsSync(marker));await until(()=>host.exitCode!==null)
 assert.equal(host.exitCode,0,errors)
 const [pid,image]=readFileSync(marker,'utf8').split('\n');newPID=Number(pid)
 assert.equal(image.toLowerCase(),canonical.toLowerCase());assert.notEqual(newPID,host.pid)
 for(let i=0;i<4;i++){
   assert.equal(await healthy(`http://127.0.0.1:${projectPort}`),true)
   const after=await request(`/api/apps/${app.id}`)
   assert.equal(after.pid,before.pid);assert.equal(after.runId,before.runId)
   assert.equal((await request('/api/health')).instanceId,health.instanceId)
   await delay(250)
 }
 assert.ok(await request(`/api/apps/${app.id}/logs`))
 console.log(JSON.stringify({passed:true,checks:['wrong version leaves old shell running','new image waits for old shell exit','canonical image instead of renamed backup','same backend instance','same project PID and run','project HTTP and logs remain available'],dir}))
} finally {
 // This server and its data are disposable fixtures, never the user's 17654/17655.
 try {await request('/api/desktop/shutdown',{})}catch{}
 if(host.exitCode===null)host.kill()
 if(newPID){try{process.kill(newPID)}catch{}}
 if(backendPID){await delay(1000);try{process.kill(backendPID)}catch{}}
}
