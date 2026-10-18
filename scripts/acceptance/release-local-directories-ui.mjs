import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..')
const {chromium}=createRequire(import.meta.url)(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const dir=path.join(root,'.tmp/release-local-directories-ui')
const evidence=path.join(root,'outputs/local-file-summary')
await mkdir(dir,{recursive:true});await mkdir(evidence,{recursive:true})
const source='/@fs/'+root.replaceAll('\\','/')+'/src'
await writeFile(path.join(dir,'index.html'),'<html lang="zh"><meta charset="utf-8"><div id="app"></div><script type="module" src="/main.ts"></script></html>')
await writeFile(path.join(dir,'main.ts'),`
import {createApp,h,ref} from 'vue';
import Panel from '${source}/components/ReleaseSafetyPanel.vue';
import {setLocale} from '${source}/i18n/index.ts';
import '${source}/styles.css';setLocale('zh-CN');
const files=Array.from({length:5000},(_,i)=>({path:'artifacts/backup/before-src/file-'+i+'.ts',category:'local',contentFingerprint:String(i),reasons:[],tracked:false}));
files.push({path:'notes.txt',category:'local',reasons:[],tracked:false},{path:'src/main.ts',category:'recommend',reasons:[],tracked:true});
createApp({setup(){const selected=ref({'src/main.ts':true});return()=>h('main',{style:'max-width:850px;margin:24px auto;padding:16px'},[
h(Panel,{appId:'fixture',intent:'formal',files,selected:selected.value,decisions:[],candidate:null,busy:false,stale:false,onChoose:(f,include)=>{selected.value={...selected.value,[f.path]:include}}}),
h('button',{onClick:()=>setLocale('en')},'English')])}}).mount('#app');`)
const server=await createServer({configFile:false,root:dir,plugins:[vue()],resolve:{alias:{'@':path.join(root,'src')}},server:{host:'127.0.0.1',port:0,fs:{allow:[root]}}})
let browser
try{
 await server.listen()
 const origin='http://127.0.0.1:'+server.httpServer.address().port
 browser=await chromium.launch({channel:'msedge',headless:true})
 const page=await browser.newPage({viewport:{width:1100,height:850}})
 const errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.route('**/*',route=>route.request().url().startsWith(origin+'/')?route.continue():route.abort())
 await page.goto(origin)
 await page.getByRole('tab',{name:/默认不提交/}).click()
 assert.equal(await page.locator('.directory-row').count(),2)
 assert.equal(await page.locator('.scope-file').count(),0,'collapsed directories must not mount thousands of files')
 await page.screenshot({path:path.join(evidence,'directories.png'),fullPage:true})
 await page.getByRole('button',{name:/artifacts\/backup\/before-src/}).click()
 assert.equal(await page.locator('.scope-file').count(),50)
 const first=page.locator('.scope-file input').first();await first.check()
 await page.getByRole('button',{name:'下一页',exact:true}).click()
 assert.equal(await page.locator('.scope-file').count(),50)
 assert.match(await page.locator('.scope-file').first().innerText(),/file-50\.ts/)
 await page.getByRole('button',{name:'上一页',exact:true}).click()
 assert.equal(await first.isChecked(),true,'paging preserves individual selection')
 await page.getByRole('button',{name:/返回目录/}).click()
 assert.match(await page.getByRole('button',{name:/artifacts\/backup\/before-src/}).innerText(),/已选 1/)
 await page.getByRole('tab',{name:/建议保存/}).click()
 assert.match(await page.locator('.scope-file').innerText(),/src\/main.ts/)
 assert.equal(await page.locator('.scope-file input').isChecked(),true)
 await page.getByRole('tab',{name:/默认不提交/}).click()
 assert.equal(await page.locator('.scope-file').count(),0)
 await page.setViewportSize({width:390,height:850})
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
 await page.screenshot({path:path.join(evidence,'directories-narrow.png'),fullPage:true})
 await page.getByRole('button',{name:'English',exact:true}).click()
 assert.equal(await page.getByRole('tab',{name:/Excluded by default/}).count(),1)
 assert.deepEqual(errors,[])
 await writeFile(path.join(evidence,'ui-verification.json'),JSON.stringify({passed:true,fixtureFiles:5002,collapsedFileRows:0,maximumExpandedFileRows:50,checks:['directory counts','pagination','selection retained','tab switching','narrow viewport','English'],errors},null,2))
 console.log('PASS: 5002 files, directory summaries, 50-row paging, selection retained, narrow viewport, English; no live API')
}finally{await browser?.close();await server.close()}
