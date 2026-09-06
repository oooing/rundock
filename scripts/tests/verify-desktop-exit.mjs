// Real Windows lifecycle test. Only isolated fixture data / disposable projects.
// Build sidecar/.tmp/desktop-exit-backend.exe and .tmp/desktop-exit-host.exe first.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs';
import path from 'node:path';
import net from 'node:net';
const root=process.cwd(), dir=mkdtempSync(path.join(root,'.tmp','desktop-exit-'));
const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function port(){const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p;}
async function until(fn){for(let i=0;i<160;i++){if(await fn())return;await delay(250);}throw Error('Timed out');}
const apiPort=await port(), projectPort=await port(), base=`http://127.0.0.1:${apiPort}`;
async function request(url,body){const res=await fetch(base+url,{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(30000)});assert.ok(res.ok,`${url}: ${res.status} ${res.ok?'':await res.text()}`);return res.json();}
const healthy=async url=>{try{return (await fetch(url,{signal:AbortSignal.timeout(1000)})).ok}catch{return false}};
const hostPath=path.join(root,'.tmp/desktop-exit-host.exe');
const host=spawn(hostPath,['keep',`${apiPort}`,path.join(root,'sidecar/.tmp/desktop-exit-backend.exe'),path.join(dir,'data'),path.join(dir,'backend.log')],{windowsHide:true,stdio:['pipe','pipe','pipe']});
let output='',errors='';host.stdout.on('data',b=>output+=b);host.stderr.on('data',b=>errors+=b);
let backendPID;
try {
 await until(()=>healthy(base+'/api/health'));
 backendPID=Number(output.trim());assert.ok(backendPID>0);
 const cwd=path.join(dir,'project');mkdirSync(cwd);
 writeFileSync(path.join(cwd,'server.cjs'),`require('http').createServer((q,s)=>s.end('alive')).listen(${projectPort},'127.0.0.1');console.log('fixture ready');`);
 writeFileSync(path.join(cwd,'start.bat'),`@echo off\r\n"${process.execPath}" "%~dp0server.cjs"\r\n`);
 const imported=await request('/api/import',{scriptPath:path.join(cwd,'start.bat')});
 const {entryScript,adapterType,cmd,args,env,scriptHash}=imported;
 const app=await request('/api/apps',{name:'Exit fixture',cwd,entryScript,adapterType,cmd,args,env,scriptHash,portHints:[projectPort]});
 await request(`/api/apps/${app.id}/start`,{});
 await until(()=>healthy(`http://127.0.0.1:${projectPort}`));
 await until(async()=>['running','degraded'].includes((await request(`/api/apps/${app.id}`)).status));
 const before=await request(`/api/apps/${app.id}`);
 host.stdin.end('keep\n');await until(()=>host.exitCode!==null);
 assert.equal(host.exitCode,0,errors);
 assert.equal(await healthy(`http://127.0.0.1:${projectPort}`),true,'project survives shell exit');
 const after=await request(`/api/apps/${app.id}`);
 assert.equal(after.pid,before.pid);assert.equal(after.runId,before.runId);
 const logs=await request(`/api/apps/${app.id}/logs`);assert.ok(logs,'logs remain available');
 // Reopened shell has no Child handle to old backend; shutdown must still work.
 const stop=spawn(hostPath,['stop',`${apiPort}`],{windowsHide:true,stdio:'pipe'});
 let stopErr='';stop.stderr.on('data',b=>stopErr+=b);
 await until(()=>stop.exitCode!==null);assert.equal(stop.exitCode,0,stopErr);
 await until(async()=>!await healthy(base+'/api/health'));
 await until(async()=>!await healthy(`http://127.0.0.1:${projectPort}`));
 await until(()=>{try{process.kill(backendPID,0);return false}catch{return true}});
 console.log(JSON.stringify({passed:true,checks:['native shell exits','ConPTY project stays alive','same PID and run preserved after reconnect','logs available','reopened shell stops project and backend'],dir}));
} finally {
 if(host.exitCode===null)host.kill();
 if(backendPID){try{process.kill(backendPID)}catch{}}
}
