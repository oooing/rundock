// Run from the repository root after: cd sidecar && go build -o .tmp/recovery-validation-backend.exe ./cmd/launcher-sidecar
// Uses isolated data and fixture projects. Never targets the installed application.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs';
import path from 'node:path';
import net from 'node:net';
const root=process.cwd();const dir=mkdtempSync(path.join(root,'.tmp','recovery-'));
const delay=ms=>new Promise(r=>setTimeout(r,ms));
const children=[];
const node='C:/Program Files/nodejs/node.exe';
function child(file,args,env={}){const p=spawn(file,args,{cwd:root,windowsHide:true,env:{...process.env,...env},stdio:['pipe','pipe','pipe']});children.push(p);p.output='';p.stdout.on('data',b=>p.output+=b);p.stderr.on('data',b=>p.output+=b);return p;}
async function port(){const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p;}
const apiPort=await port();const base=`http://127.0.0.1:${apiPort}`;
async function request(url,body,method){const r=await fetch(base+url,{method:method||(body?'POST':'GET'),headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});return {status:r.status,body:await r.json()};}
async function until(fn){for(let i=0;i<120;i++){const v=await fn();if(v)return v;await delay(250);}throw Error('wait timed out');}
async function makeApp(name,listenPort){
 const cwd=path.join(dir,name);mkdirSync(cwd);
 writeFileSync(path.join(cwd,'server.cjs'),`require('http').createServer((q,s)=>s.end('ok')).listen(${listenPort},'127.0.0.1');`);
 writeFileSync(path.join(cwd,'start.bat'),`@echo off\r\n"${node}" "%~dp0server.cjs"\r\n`);
 const imported=await request('/api/import',{scriptPath:path.join(cwd,'start.bat')});assert.equal(imported.status,200,JSON.stringify(imported));
 const {entryScript,adapterType,cmd,args,env,scriptHash}=imported.body;
 const app=await request('/api/apps',{name,cwd,entryScript,adapterType,cmd,args,env,scriptHash,portHints:[listenPort]});
 assert.equal(app.status,201,JSON.stringify(app));return {...app.body,cwd};
}
async function failApp(a){const started=await request(`/api/apps/${a.id}/start`,{});assert.equal(started.status,200,JSON.stringify(started));await until(async()=> (await request(`/api/apps/${a.id}`)).body.status==='failed');return (await request(`/api/apps/${a.id}/startup-issue`)).body;}
let backend;
try{
 backend=child(path.join(root,'sidecar/.tmp/recovery-validation-backend.exe'),['-port',`${apiPort}`],{LAUNCHER_DATA_DIR:path.join(dir,'data')});
 await until(async()=>{try{return (await request('/api/health')).status===200}catch{return false}});
 const occupied=await port();const a=await makeApp('project',occupied);
 let owner=child(node,[path.join(a.cwd,'server.cjs')]);await until(async()=>{try{return (await fetch(`http://127.0.0.1:${occupied}`)).ok}catch{return false}});
 let issue=await failApp(a);assert.equal(issue.code,'port_in_use',JSON.stringify(issue));assert.equal(issue.canRecover,true,JSON.stringify(issue));assert.equal(issue.conflicts[0].pid,owner.pid);
 const stale=await request(`/api/apps/${a.id}/recover-ports`,{fingerprint:'stale'});assert.equal(stale.status,409);assert.equal(owner.exitCode,null);
 const recovered=await request(`/api/apps/${a.id}/recover-ports`,{fingerprint:issue.fingerprint});assert.equal(recovered.status,200,JSON.stringify(recovered));await until(()=>owner.exitCode!==null);
 await until(async()=>['running','degraded'].includes((await request(`/api/apps/${a.id}`)).body.status));
 assert.equal((await request(`/api/apps/${a.id}/stop`,{})).status,200);
 const unrelatedPort=await port();const b=await makeApp('other',unrelatedPort);const c=await makeApp('blocked',unrelatedPort);
 const unrelated=child(node,[path.join(b.cwd,'server.cjs')]);await until(async()=>{try{return (await fetch(`http://127.0.0.1:${unrelatedPort}`)).ok}catch{return false}});
 issue=await failApp(c);assert.equal(issue.canRecover,false,JSON.stringify(issue));assert.equal((await request(`/api/apps/${c.id}/recover-ports`,{fingerprint:issue.fingerprint})).status,409);assert.equal(unrelated.exitCode,null);
 const self=await makeApp('self',apiPort);issue=await failApp(self);assert.equal(issue.canRecover,false);assert.equal((await request('/api/health')).status,200);
 console.log(JSON.stringify({passed:true,checks:['real port conflict detected','project process identity verified','stale confirmation rejected','same-project owner terminated and app restarted','other project protected','serving backend protected','stop after recovery'],dir,apiPort}));
}finally{
 for(const p of children.reverse())if(p.exitCode===null)p.kill();
}
