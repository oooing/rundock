// Render the actual Vue dialog/store in an isolated browser. Native IPC is a fixture.
import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import {createServer} from 'vite'
import vue from '@vitejs/plugin-vue'
const require=createRequire(import.meta.url)
const {chromium}=require(process.env.RUNDOCK_PLAYWRIGHT_MODULE||'playwright')
const root=process.cwd(), dir=path.join(root,'.tmp',`update-ui-${Date.now()}`)
fs.mkdirSync(dir,{recursive:true})
fs.writeFileSync(path.join(dir,'index.html'),'<html><body><div id="app"></div><script type="module" src="/main.js"></script></body></html>')
fs.writeFileSync(path.join(dir,'core.js'),`export class Channel{};export async function invoke(cmd,args){window.calls.push({cmd,args});if(cmd==='update_install_plan')return {fingerprint:'shown-list',projects:[{id:'a',name:'演示网站'},{id:'b',name:'演示后台'}]};if(cmd==='install_app_update')return new Promise(()=>{});return null;}`)
fs.writeFileSync(path.join(dir,'main.js'),`import {createApp,h} from 'vue';import Dialog from '@/components/AppUpdateDialog.vue';import * as store from '@/stores/appUpdate';import '@/styles.css';window.calls=[];window.store=store;Object.assign(store.appUpdate,{phase:'ready',info:{version:'2.0.42',size:100,notes:'',url:''},dialogOpen:true});createApp({render:()=>store.appUpdate.dialogOpen?h(Dialog):h('div','已关闭')}).mount('#app')`)
const server=await createServer({configFile:false,root:dir,plugins:[vue()],resolve:{alias:[{find:'@',replacement:path.join(root,'src')},{find:'@tauri-apps/api/core',replacement:path.join(dir,'core.js')}]},server:{host:'127.0.0.1',port:0,fs:{allow:[root]}}})
let browser
try{
 await server.listen();browser=await chromium.launch({channel:'msedge',headless:true})
 const page=await browser.newPage({locale:'zh-CN',viewport:{width:1000,height:760}}), errors=[]
 page.on('pageerror',e=>errors.push(e.message));await page.goto(server.resolvedUrls.local[0])
 await page.getByRole('dialog').waitFor()
 await page.evaluate(()=>{window.store.appUpdate.phase='installed';window.store.appUpdate.info.notes='本次更新优化了安装流程。'})
 await page.getByRole('button',{name:'立即重启',exact:true}).waitFor()
 await page.screenshot({path:path.join(root,'outputs/acceptance/update-compact-installed.png')})
 await page.evaluate(()=>{window.store.appUpdate.phase='ready';window.store.appUpdate.info.notes=''})
 const choice=page.getByRole('button',{name:'下次启动安装',exact:true});await choice.waitFor()
 assert.match(await choice.getAttribute('class'),/primary/)
 await choice.click();await page.getByText('已安排安装',{exact:true}).waitFor()
 assert.deepEqual(await page.evaluate(()=>window.calls.map(c=>c.cmd)),['schedule_app_update'])
 await page.getByRole('button',{name:'立即安装',exact:true}).click();await page.getByText('确认安装',{exact:true}).waitFor()
 await page.getByText('演示网站',{exact:true}).waitFor();await page.getByText('演示后台',{exact:true}).waitFor()
 assert.ok((await page.getByRole('dialog').innerText()).includes('请先保存工作'))
 assert.equal(await page.evaluate(()=>window.calls.some(c=>c.cmd==='install_app_update')),false)
 await page.screenshot({path:path.join(root,'outputs/acceptance/update-choice-confirm.png')})
 await page.getByRole('button',{name:'停止项目并安装',exact:true}).click()
 await page.getByText('完成后自动恢复项目，请稍候。',{exact:true}).waitFor()
 assert.equal(await page.getByRole('button',{name:'关闭',exact:true}).isDisabled(),true)
 await page.keyboard.press('Escape');assert.equal(await page.getByRole('dialog').count(),1)
 assert.deepEqual(await page.evaluate(()=>window.calls.at(-1)),{cmd:'install_app_update',args:{fingerprint:'shown-list'}})
 await page.evaluate(()=>Object.assign(window.store.appUpdate,{phase:'finished',error:'',report:{remaining:[{id:'b'}],failures:['演示后台：端口被占用，请停止占用程序后重试。']}}))
 await page.getByText('部分项目待恢复',{exact:true}).waitFor();await page.getByRole('button',{name:'重试恢复',exact:true}).waitFor()
 await page.screenshot({path:path.join(root,'outputs/acceptance/update-choice-failure.png')})
 assert.deepEqual(errors,[])
 console.log('PASS: default deferred action, exact stop list and explicit confirmation, install dismissal guard, partial recovery and retry visible; no real IPC invoked.')
}finally{await browser?.close();await server.close()}
