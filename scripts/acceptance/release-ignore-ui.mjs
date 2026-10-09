import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {mkdir,writeFile} from 'node:fs/promises'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {createServer} from 'vite'
import vue from '@vitejs/plugin-vue'

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..')
const {chromium}=createRequire(import.meta.url)(process.env.RUNDOCK_PLAYWRIGHT_MODULE||'playwright')
const dir=path.join(root,'.tmp/release-ignore-ui'),evidence=path.join(root,'outputs/release-ignore-ui')
await mkdir(dir,{recursive:true});await mkdir(evidence,{recursive:true})
const source='/@fs/'+root.replaceAll('\\','/')+'/src'
await writeFile(path.join(dir,'index.html'),'<html lang="zh"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
await writeFile(path.join(dir,'main.ts'),[
 "import {createApp,h,ref} from 'vue';",
 "import Panel from '"+source+"/components/ReleaseSafetyPanel.vue';",
 "import {setLocale} from '"+source+"/i18n/index.ts';",
 "import '"+source+"/styles.css';setLocale('zh-CN');",
 "const file={path:'notes/本地笔记 [a].txt',category:'review',contentFingerprint:'fixture',tracked:false,reasons:[]};",
 "createApp({setup(){const files=ref([file]),decisions=ref([]),mode=ref('normal'),calls=ref(0),locked=ref(false);return()=>h('main',{style:'max-width:850px;margin:24px auto;padding:16px'},[",
 "h('h2','文件处理 · 隔离验收'),...['normal','error','tracked'].map(m=>h('button',{onClick:()=>{mode.value=m;files.value=[{...file,tracked:m==='tracked'}];decisions.value=[]}},m)),h('output',{id:'calls'},String(calls.value)),",
 "h(Panel,{key:mode.value,appId:'fixture',intent:'formal',files:files.value,selected:{},decisions:decisions.value,candidate:null,busy:false,locked:locked.value,stale:false,",
 "ignoreFile:async f=>{calls.value++;locked.value=true;await new Promise(r=>setTimeout(r,150));locked.value=false;if(mode.value==='error')throw new Error('文件内容已变化，请刷新后重新确认忽略');if(f.path!==file.path||f.contentFingerprint!=='fixture')throw new Error('Wrong file');files.value=[];return '.gitignore'},",
 "onChoose:(f,included)=>decisions.value=[{path:f.path,contentFingerprint:f.contentFingerprint,decision:included?'include':'exclude'}]})])}}).mount('#app');",
].join('\n'))
const server=await createServer({configFile:false,root:dir,plugins:[vue()],resolve:{alias:{'@':path.join(root,'src')}},server:{host:'127.0.0.1',port:19485,strictPort:false,fs:{allow:[root]}}})
let browser
try{
 await server.listen()
 const origin='http://127.0.0.1:'+server.httpServer.address().port
 browser=await chromium.launch({channel:'msedge',headless:true})
 const page=await browser.newPage({viewport:{width:1100,height:850}}),errors=[]
 page.on('pageerror',e=>errors.push(e.message))
 await page.route('**/*',route=>route.request().url().startsWith(origin+'/')?route.continue():route.abort())
 await page.goto(origin)
 const select=page.getByRole('combobox'),calls=page.locator('#calls')
 await select.selectOption('ignore')
 await page.getByRole('button',{name:'待确认项本次均不提交',exact:true}).click()
 assert.equal(await calls.innerText(),'0');assert.equal(await page.locator('.ignore-confirm').count(),0)
 assert.equal(await select.inputValue(),'exclude')
 await select.selectOption('exclude')
 assert.equal(await calls.innerText(),'0')
 await page.getByText('仅排除本次改动，下次发布仍可能提示。').waitFor()
 await select.selectOption('ignore')
 await page.getByRole('button',{name:'取消',exact:true}).click()
 assert.equal(await calls.innerText(),'0');assert.equal(await select.inputValue(),'exclude')
 await select.selectOption('ignore')
 const confirm=page.getByRole('button',{name:'确认加入忽略清单',exact:true})
 await confirm.focus();assert.equal(await confirm.evaluate(el=>el===document.activeElement),true)
 await page.screenshot({path:path.join(evidence,'confirm.png'),fullPage:true})
 await confirm.press('Enter')
 await page.locator('.ignore-notice').waitFor()
 assert.equal(await calls.innerText(),'1');assert.equal(await select.count(),0)
 await page.getByRole('button',{name:'error',exact:true}).click()
 await select.selectOption('ignore');await confirm.click()
 await page.getByRole('alert').waitFor()
 assert.equal(await calls.innerText(),'2');assert.equal(await select.count(),1)
 assert.match(await page.getByRole('alert').innerText(),/文件内容已变化/)
 await page.setViewportSize({width:375,height:812})
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
 await page.screenshot({path:path.join(evidence,'narrow-error.png'),fullPage:true})
 await page.getByRole('button',{name:'tracked',exact:true}).click()
 assert.equal(await page.locator('option[value="ignore"]').count(),0)
 await page.getByText('已被 Git 跟踪，不能直接永久忽略；本次不提交不会移除远端文件。').waitFor()
 assert.deepEqual(errors,[])
 console.log('PASS: one-time exclude, cancel, keyboard confirmation, persistent-ignore success/error, tracked restriction, narrow layout; no live API')
}finally{await browser?.close();await server.close()}
