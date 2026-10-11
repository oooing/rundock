// Isolated real API/Vite/process acceptance. Never uses the user's RunDock DB.
// Build sidecar/.tmp/temporary-port-validation.exe first. --serve keeps a demo
// available for manual/CUA browser acceptance; Ctrl+C stops only these fixtures.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..')
const dir=path.join(root,'sidecar/.tmp',`temporary-port-${Date.now()}`)
mkdirSync(dir,{recursive:true})
const checks=[],children=[],apps=[]
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms))
const hash=file=>createHash('sha256').update(readFileSync(file)).digest('hex')
const config=a=>JSON.stringify([a.cmd,a.args,a.env,a.portHints,a.healthUrl,a.scriptHash,a.cwd,a.entryScript])
async function freePort(){const server=net.createServer();await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve)});const port=server.address().port;await new Promise(resolve=>server.close(resolve));return port}
async function until(check,ms=40000){const end=Date.now()+ms;while(Date.now()<end){const result=await check();if(result)return result;await pause(250)};throw Error('Acceptance timed out')}
const apiPort=await freePort(),uiPort=await freePort(),originalPort=await freePort()
const base=`http://127.0.0.1:${apiPort}`,uiBase=`http://127.0.0.1:${uiPort}`
async function raw(route,method='GET',body,headers={}){const response=await fetch(base+route,{method,headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(30000)});return {status:response.status,body:await response.json()}}
async function request(...args){const result=await raw(...args);assert.ok(result.status<300,JSON.stringify(result));return result.body}
const action=(app,op,body={})=>request(`/api/apps/${app.id}/${op}`,'POST',body)
const view=app=>request(`/api/apps/${app.id}`)
const running=app=>until(async()=>{const current=await view(app);return current.status==='running'&&current.runId&&current})
function put(name,file,contents){const folder=path.join(dir,name);mkdirSync(folder,{recursive:true});const result=path.join(folder,file);writeFileSync(result,contents);return result}
async function makeApp(name,entry,healthUrl){const candidate=await request('/api/import','POST',{scriptPath:entry});const app=await request('/api/apps','POST',{name,healthUrl,entryScript:candidate.entryScript,cwd:candidate.cwd,adapterType:candidate.adapterType,cmd:candidate.cmd,args:candidate.args,env:candidate.env,scriptHash:candidate.scriptHash,portHints:candidate.portHints});apps.push(app);return app}
async function ownerStillRunning(owner,before){const now=await view(owner);assert.equal(now.status,'running');assert.equal(now.pid,before.pid);assert.equal(now.runId,before.runId);assert.equal(await(await fetch(`http://127.0.0.1:${originalPort}`)).text(),'A remains running')}
let backend,vite,owner,target,unsupported
let demoReady=false,cleaning=false
async function cleanup(){if(cleaning)return;cleaning=true;for(const app of apps){try{const current=await view(app);if(current.runId)await action(app,'stop')}catch{}};await vite?.close();for(const child of children)if(child.exitCode===null)child.kill()}
try{
  backend=spawn(path.join(root,'sidecar/.tmp/temporary-port-validation.exe'),['-port',String(apiPort)],{cwd:dir,windowsHide:true,env:{...process.env,LAUNCHER_DATA_DIR:path.join(dir,'data'),LAUNCHER_UI_ORIGIN:uiBase},stdio:['ignore','ignore','pipe']});children.push(backend)
  backend.stderr.on('data',chunk=>{backend.lastError=((backend.lastError||'')+chunk).slice(-3000)})
  await until(async()=>{try{return (await request('/api/health')).status==='ok'}catch{return false}})
  const ownerEntry=put('A','start.cmd',`@echo off\r\nrem rundock:ready http://127.0.0.1:${originalPort}/\r\n"${process.execPath}" "%~dp0server.cjs"\r\n`)
  put('A','package.json',JSON.stringify({name:'isolated-owner',private:true,scripts:{start:'node server.cjs'}}))
  put('A','server.cjs',`require('node:http').createServer((req,res)=>res.end('A remains running')).listen(${originalPort},'127.0.0.1',()=>console.log('http://127.0.0.1:${originalPort}'));`)
  owner=await makeApp('A · 持续运行',ownerEntry,`http://127.0.0.1:${originalPort}/`)
  await action(owner,'start');const aBefore=await running(owner)
  const definition={version:1,adapter:'vite',script:'dev',port:originalPort,host:'127.0.0.1',interfaces:'same-origin'}
  const bEntry=put('B','package.json',JSON.stringify({name:'temporary-vite-fixture',private:true,type:'module',scripts:{dev:'vite --config vite.config.mjs'},rundock:{temporaryPort:definition}},null,2))
  put('B','vite.config.mjs',`import {defineConfig} from 'vite';export default defineConfig({server:{host:'127.0.0.1',port:${originalPort},strictPort:true},plugins:[{name:'same-origin-api',configureServer(server){server.middlewares.use('/api/ping',(req,res)=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify({ok:true,host:req.headers.host}))})}}]});`)
  put('B','index.html','<!doctype html><html><head><title>Temporary Vite B</title></head><body><h1>B on temporary port</h1></body></html>')
  target=await makeApp('B · 支持临时端口',bEntry,`http://127.0.0.1:${originalPort}/api/ping`)
  const configBefore=config(target),fileBefore=hash(bEntry)
  let plan=await action(target,'port-resolution')
  assert.equal(plan.state,'conflict');assert.equal(plan.temporaryPort.supported,true);assert.equal(plan.conflicts[0].managedAppId,owner.id)
  assert.equal((await raw(`/api/apps/${target.id}/temporary-port-start`,'POST',{confirmationToken:plan.confirmationToken})).status,409)
  assert.equal((await raw(`/api/apps/${target.id}/temporary-port-start`,'POST',{confirmationToken:plan.temporaryPort.confirmationToken},{Origin:'https://untrusted.example'})).status,403)
  await ownerStillRunning(owner,aBefore);checks.push('close tokens and untrusted origins cannot trigger temporary start')
  const port=plan.temporaryPort.port
  await action(target,'temporary-port-start',{confirmationToken:plan.temporaryPort.confirmationToken})
  let b=await running(target)
  assert.deepEqual(b.temporaryPort,{originalPort,port,host:'127.0.0.1'})
  assert.equal(config(b),configBefore);assert.equal(hash(bEntry),fileBefore)
  const ping=await(await fetch(`http://127.0.0.1:${port}/api/ping`)).json()
  assert.equal(ping.ok,true);assert.equal(ping.host,`127.0.0.1:${port}`)
  assert.ok(b.services.some(service=>service.port===port&&service.health==='healthy'&&service.healthProbeUrl===`http://127.0.0.1:${port}/api/ping`))
  await ownerStillRunning(owner,aBefore);checks.push('real B starts on new port with same-origin API and remapped health; A PID/run unchanged')
  assert.equal((await raw(`/api/apps/${target.id}/temporary-port-start`,'POST',{confirmationToken:plan.temporaryPort.confirmationToken})).status,409)
  await action(target,'stop')
  assert.ok((await raw(`/api/apps/${target.id}/start`,'POST',{})).status>=400)
  await ownerStillRunning(owner,aBefore);checks.push('normal start restores original port, blocks conflict, does not stop A')
  plan=await action(target,'port-resolution')
  const stolen=net.createServer();await new Promise((resolve,reject)=>{stolen.once('error',reject);stolen.listen(plan.temporaryPort.port,'127.0.0.1',resolve)})
  try{const result=await raw(`/api/apps/${target.id}/temporary-port-start`,'POST',{confirmationToken:plan.temporaryPort.confirmationToken});assert.equal(result.status,409);assert.match(result.body.error,/临时端口已不可用/);await ownerStillRunning(owner,aBefore)}finally{await new Promise(resolve=>stolen.close(resolve))}
  checks.push('candidate port stolen before confirmation fails closed without stopping A')
  await action(owner,'stop');await action(target,'start');b=await running(target)
  assert.equal(b.temporaryPort,undefined);assert.ok(b.services.some(service=>service.port===originalPort));assert.equal(config(b),configBefore);assert.equal(hash(bEntry),fileBefore)
  checks.push('after A stops, ordinary B start uses original port; original config and file unchanged')
  await action(target,'stop');await action(owner,'start');await running(owner)
  const unsupportedEntry=put('unsupported','package.json',JSON.stringify({name:'not-adapted',scripts:{dev:'vite'}}))
  unsupported=await makeApp('C · 未声明接口兼容',unsupportedEntry,`http://127.0.0.1:${originalPort}/`)
  const unsupportedPlan=await action(unsupported,'port-resolution')
  assert.equal(unsupportedPlan.temporaryPort.supported,false);assert.match(unsupportedPlan.temporaryPort.reason,/未声明/);assert.equal(unsupportedPlan.temporaryPort.confirmationToken,undefined)
  checks.push('unsupported project explains API compatibility reason and receives no executable token')
  writeFileSync(path.join(dir,'report.json'),JSON.stringify({passed:true,checks,base,uiBase,originalPort,targetId:target.id,unsupportedId:unsupported.id},null,2))
  console.log(JSON.stringify({passed:true,checks,report:path.join(dir,'report.json')}))
  if(process.argv.includes('--serve')){
    const source='/@fs/'+root.replaceAll('\\','/')+'/src'
    const harness=path.join(dir,'ui');mkdirSync(harness)
    writeFileSync(path.join(harness,'index.html'),'<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
    writeFileSync(path.join(harness,'main.ts'),`import {createApp,h,ref} from 'vue';import {createPinia} from 'pinia';import PortDialog from '${source}/components/PortResolutionDialog.vue';import AppCard from '${source}/components/AppCard.vue';import {useAppsStore} from '${source}/stores/apps.ts';import '${source}/styles.css';window.__LAUNCHER_BASE__=${JSON.stringify(base)};localStorage.setItem('rundock.ui.locale','zh-CN');const choice=ref(${JSON.stringify(target.id)});createApp({setup(){const store=useAppsStore();store.load();const dialog=ref();return()=>h('main',{style:'padding:36px;max-width:1100px;margin:auto'},[h('h1','临时端口 · 隔离验收'),h('p','以下为独立测试项目，不操作用户当前项目。'),h('div',{style:'display:flex;gap:12px;margin:20px 0'},[h('button',{onClick:()=>{choice.value=${JSON.stringify(target.id)}}},'支持的项目 B'),h('button',{onClick:()=>{choice.value=${JSON.stringify(unsupported.id)}}},'不支持的项目 C'),h('button',{onClick:()=>dialog.value.open()},'查看端口方案')]),h(PortDialog,{ref:dialog,appId:choice.value,appName:choice.value===${JSON.stringify(target.id)}?'B · 支持临时端口':'C · 未声明接口兼容'}),...store.apps.map(app=>h('div',{style:'max-width:480px;margin:16px 0'},[h(AppCard,{app,groups:[],onStop:async()=>{await store.stop(app.id)},onStart:async()=>{await store.start(app.id)}})]))])}}).use(createPinia()).mount('#app');`)
    vite=await createServer({configFile:false,root:harness,plugins:[vue()],resolve:{alias:{'@':path.join(root,'src')}},server:{host:'127.0.0.1',port:uiPort,strictPort:true,fs:{allow:[root]}}});await vite.listen()
    demoReady=true;console.log(JSON.stringify({demo:uiBase,base,dir,targetId:target.id,unsupportedId:unsupported.id}))
    await new Promise(resolve=>{process.once('SIGINT',resolve);process.once('SIGTERM',resolve)})
  }
}catch(error){writeFileSync(path.join(dir,'report.json'),JSON.stringify({passed:false,checks,error:String(error),backendError:backend?.lastError},null,2));console.error(error);process.exitCode=1}
finally{await cleanup()}
