// Real Windows processes + native Rust transaction + NSIS hooks. Isolated data only.
import assert from 'node:assert/strict'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import fs from 'node:fs'
import path from 'node:path'
import net from 'node:net'
import { createHash } from 'node:crypto'
const exec = promisify(execFile), root = process.cwd()
const directory = path.join(root, '.tmp', `update-transaction-${Date.now()}`)
const installed = path.join(directory, 'installed 中文'), data = path.join(directory, 'data')
fs.mkdirSync(installed, {recursive:true}); fs.mkdirSync(data, {recursive:true})
const checks=[], children=[], apps=[]
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
async function until(fn, timeout=30000) {const end=Date.now()+timeout;while(Date.now()<end){try{const r=await fn();if(r)return r}catch{}await sleep(200)}throw Error('Timed out: '+fn)}
async function freePort(){const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p}
const port=await freePort(), base=`http://127.0.0.1:${port}`
const hash=p=>createHash('sha256').update(fs.readFileSync(p)).digest('hex')
const probe=path.join(root,'src-tauri/target/debug/examples/update-transaction-probe.exe')
const run=(file,args=[])=>exec(file,args,{cwd:directory,windowsHide:true,timeout:180000,maxBuffer:2e6})
const cli=op=>run(probe,[op,String(port),data])
const reportFile=path.join(data,'desktop-update.json')
const report=()=>JSON.parse(fs.readFileSync(reportFile,'utf8'))
const save=p=>fs.writeFileSync(reportFile,JSON.stringify(p))
async function raw(route,method='GET',body){const r=await fetch(base+route,{method,headers:{'content-type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(45000)});return {status:r.status,body:await r.json()}}
async function api(...args){const r=await raw(...args);assert.ok(r.status<300,JSON.stringify(r));return r.body}
const view=a=>api(`/api/apps/${a.id}`)
const action=(a,op)=>api(`/api/apps/${a.id}/${op}`,'POST',{})
const running=a=>until(async()=>{const v=await view(a);return v.status==='running'&&v.pid&&v})
function pass(message){checks.push(message);console.log('PASS:',message)}
let backend
async function startBackend(){backend=spawn(path.join(installed,'launcher-sidecar.exe'),['-port',String(port)],{cwd:directory,windowsHide:true,env:{...process.env,LAUNCHER_DATA_DIR:data},stdio:['ignore','ignore','pipe']});children.push(backend);backend.stderr.on('data',b=>fs.appendFileSync(path.join(directory,'backend.log'),b));await until(()=>api('/api/health'));return api('/api/health')}
async function makeProject(name,refuses=false){const folder=path.join(directory,name);fs.mkdirSync(folder);const p=await freePort(),entry=path.join(folder,'start.cmd'),server=path.join(folder,'server.cjs');fs.writeFileSync(entry,'@echo off\r\n');fs.writeFileSync(server,`const s=require('http').createServer((q,r)=>r.end('${name}')).listen(${p},'127.0.0.1',()=>console.log('http://127.0.0.1:${p}/'));process.on('SIGINT',()=>{${refuses?'console.log("refusing graceful stop")':'s.close(()=>process.exit(0))'}});`);const a=await api('/api/apps','POST',{name,entryScript:entry,cwd:folder,adapterType:'batch',cmd:process.execPath,args:[server],env:{},scriptHash:hash(entry),healthUrl:`http://127.0.0.1:${p}/`,portHints:[p]});apps.push(a);return {...a,entry,server,health:`http://127.0.0.1:${p}/`}}
try {
 fs.copyFileSync(path.join(root,'.tmp/update-backend-old.exe'),path.join(installed,'launcher-sidecar.exe'))
 const old=await startBackend();assert.equal(old.version,'2.0.41')
 const a=await makeProject('项目 A'), b=await makeProject('项目 B'), c=await makeProject('保持停止')
 await action(a,'start');await action(b,'start');const before=[await running(a),await running(b)]
 const plan=await api('/api/desktop/update-plan');assert.deepEqual(plan.projects.map(p=>p.name).sort(),['项目 A','项目 B'])
 const stale=await raw('/api/desktop/update-stop','POST',{fingerprint:'outdated'});assert.equal(stale.status,409);assert.equal((await view(a)).pid,before[0].pid)
 pass('停止清单只包含运行项目；过期确认不停止任何项目')
 await cli('schedule');assert.equal((await cli('can-start')).stdout.trim(),'false');assert.equal((await view(a)).pid,before[0].pid)
 pass('默认延后安装持久化；后台仍在运行时不会自动安装')
 await cli('stop');await until(()=>backend.exitCode!==null)
 for(const app of[a,b]){await assert.rejects(fetch(app.health,{signal:AbortSignal.timeout(1000)}))}
 assert.equal(report().remaining.length,2)
 pass('原运行清单先落盘；真实项目和旧后台正常停止')
 const helper=path.join(directory,'install-helper.exe'), setup=path.join(directory,'setup.exe'), nsi=path.join(directory,'fixture.nsi')
 await run('C:/Users/oooin/go/bin/go.exe',['-C',path.join(root,'sidecar'),'build','-trimpath','-ldflags','-H=windowsgui','-o',helper,'./cmd/rundock-install-helper'])
 fs.writeFileSync(nsi,`Unicode true\nRequestExecutionLevel user\nSilentInstall silent\n!include "LogicLib.nsh"\n!define RUNDOCK_INSTALL_HELPER "${helper}"\n!include "${path.join(root,'src-tauri/windows/installer-hooks.nsh')}"\nName "Isolated update transaction"\nOutFile "${setup}"\nInstallDir "${installed}"\nSection\n!insertmacro NSIS_HOOK_PREINSTALL\nSetOutPath $INSTDIR\nFile /oname=launcher-sidecar.exe "${path.join(root,'.tmp/update-backend-new.exe')}"\n!insertmacro NSIS_HOOK_POSTINSTALL\nSectionEnd\n`)
 await run(path.join(process.env.LOCALAPPDATA,'tauri/NSIS/makensis.exe'),['/V2','/INPUTCHARSET','UTF8',nsi])
 await run(setup,['/S','/UPDATE','/P']);assert.equal(hash(path.join(installed,'launcher-sidecar.exe')),hash(path.join(root,'.tmp/update-backend-new.exe')))
 const fresh=await startBackend();assert.equal(fresh.version,'2.0.42');assert.notEqual(fresh.instanceId,old.instanceId)
 await cli('restore');assert.equal(report().error,'');assert.deepEqual(report().remaining,[])
 const after=[await running(a),await running(b)]
 for(let i=0;i<2;i++){assert.notEqual(after[i].pid,before[i].pid);assert.notEqual(after[i].runId,before[i].runId)}
 for(const app of[a,b])assert.equal((await fetch(app.health)).status,200)
 assert.equal((await view(c)).status,'stopped');await cli('restore');assert.equal((await view(a)).pid,after[0].pid)
 pass('真实 NSIS 覆盖安装后，新后台生效、两项目恢复、原停止项目不启动；重复恢复不重复启动')
 await cli('schedule');await cli('deferred-exit');await until(()=>backend.exitCode!==null)
 assert.equal(report().phase,'scheduled');assert.equal(report().projects.length,2);assert.equal((await cli('can-start')).stdout.trim(),'true')
 await run(setup,['/S','/UPDATE','/P']);let p=report();p.phase='restoring';save(p)
 await startBackend();await cli('restore');assert.deepEqual(report().remaining,[]);await running(a);await running(b)
 pass('延后安装在完整退出时保存运行清单，下次启动安装后自动恢复')
 await cli('stop');await until(()=>backend.exitCode!==null)
 fs.appendFileSync(a.entry,'rem changed\r\n');await startBackend();await cli('restore')
 assert.equal(report().remaining.length,1);assert.match(report().failures[0],/项目 A.*配置或文件已变化/);await running(b);assert.equal((await view(a)).status,'stopped')
 fs.writeFileSync(a.entry,'@echo off\r\n');p=report();p.phase='restoring';save(p);await cli('restore');assert.deepEqual(report().remaining,[]);await running(a)
 pass('单项目配置变化会明确报错；修正后只恢复失败项')
 await cli('stop');await until(()=>backend.exitCode!==null);p=report();p.phase='installing';save(p);await startBackend();await cli('restore')
 assert.match(report().error,/安装被中断/);assert.deepEqual(report().remaining,[]);assert.equal((await cli('can-start')).stdout.trim(),'false')
 pass('中断安装可恢复项目；保留失败原因，不循环自动安装')
 const stubborn=await makeProject('拒绝停止',true);await action(stubborn,'start');const stubbornBefore=await running(stubborn)
 await api('/api/settings','PATCH',{grace_period_seconds:'2'})
 await assert.rejects(cli('stop'));assert.equal((await view(stubborn)).pid,stubbornBefore.pid)
 assert.match(report().error,/未正常退出/);await running(a);await running(b)
 pass('项目拒绝退出时取消安装、不强杀；已停止的其他项目恢复')
 fs.writeFileSync(path.join(root,'outputs/acceptance/update-transaction-real.json'),JSON.stringify({passed:true,at:new Date().toISOString(),directory,checks,before,after,oldBackend:old,newBackend:fresh,githubPublished:false,scope:'isolated real backend/project processes, production Rust transaction module and real NSIS hooks; desktop UI IPC covered separately'},null,2))
} finally {
 for(const app of apps){try{await action(app,'stop')}catch{}}
 try{await api('/api/desktop/shutdown','POST',{})}catch{}
 for(const child of children)if(child.exitCode===null)child.kill()
 // Keep small diagnostics; never retain per-run installers/databases indefinitely.
 if(fs.existsSync(path.join(directory,'backend.log')))fs.copyFileSync(path.join(directory,'backend.log'),path.join(root,'outputs/acceptance/update-transaction-backend.log'))
 const temporaryRoot=fs.realpathSync(path.join(root,'.tmp'))
 const resolved=fs.realpathSync(directory)
 assert.equal(path.dirname(resolved).toLowerCase(),temporaryRoot.toLowerCase())
 assert.ok(path.basename(resolved).startsWith('update-transaction-'))
 fs.rmSync(resolved,{recursive:true,force:true,maxRetries:10,retryDelay:200})
 console.log('Cleaned isolated test directory:',resolved)
}
