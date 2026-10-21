// Isolated real-component acceptance. No user project is created, started, or stopped.
import assert from 'node:assert/strict'
import {mkdirSync,writeFileSync} from 'node:fs'
import {createRequire} from 'node:module'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {createServer} from 'vite'
import vue from '@vitejs/plugin-vue'
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../..')
const output=path.join(root,'outputs/acceptance/project-import-01')
const harness=path.join(root,'.tmp','project-import-01-'+Date.now())
mkdirSync(output,{recursive:true});mkdirSync(harness,{recursive:true})
const source='/@fs/'+root.replaceAll('\\','/')+'/src'
writeFileSync(path.join(harness,'index.html'),'<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
writeFileSync(path.join(harness,'main.ts'),`
import {createApp,h,ref} from 'vue';import {createPinia} from 'pinia';
import Wizard from '${source}/components/AddProjectWizard.vue';import Preview from '${source}/components/ProjectDropPreview.vue';import {useAppsStore} from '${source}/stores/apps.ts';import '${source}/styles.css';
window.__LAUNCHER_BASE__='http://wizard.fixture.invalid';localStorage.setItem('rundock.ui.locale','zh-CN');
createApp({setup(){const shown=ref(false),hover=ref(false),wizard=ref(null),store=useAppsStore();store.load();window.store=store;
window.receiveDrop=p=>wizard.value.receiveDrop([p]);return()=>h('main',{style:'--import-sidebar-width:208px;--workspace-header-height:100px'},[
h('button',{id:'open',onClick:()=>shown.value=true},'添加项目'),h('button',{id:'preview',onClick:()=>hover.value=!hover.value},'preview'),
hover.value?h(Preview):null,shown.value?h(Wizard,{ref:wizard,onClose:()=>shown.value=false,onAdded:()=>shown.value=false}):null,
h('ol',{id:'projects'},store.apps.map(a=>h('li',{key:a.id},a.name)))])}}).use(createPinia()).mount('#app');`)
const report={passed:false,boundary:'Actual Vue components and store, isolated HTTP fixtures; no OS drag or user project mutation',checks:[]}
let server,browser,page,releaseSlow
const oldApps=[{id:'old-a',name:'Existing A',sortOrder:-3,entryScript:'C:/old/a.cmd'},{id:'old-b',name:'Existing B',sortOrder:2,entryScript:'C:/old/b.cmd'}]
const saved=[...oldApps],mutations=[],errors=[]
let imports=0,mode='single',aSlow=false,bFail=false,aArrived
const arrived=new Promise(r=>aArrived=r),gate=new Promise(r=>releaseSlow=r)
const candidate=entry=>({name:'Project '+entry,entryScript:'C:/fixture/'+entry+'.cmd',cwd:'C:/fixture/'+entry,projectRoot:'C:/fixture',adapterType:'batch',cmd:'cmd.exe',args:['/c',entry+'.cmd'],env:{FIXTURE_ENTRY:entry},portHints:[entry==='A'?18211:18212],scriptHash:'fixture-'+entry,markers:[entry+'-marker'],findings:entry==='A'?[{level:'warn',message:'Only A warning'}]:[]})
try{
 server=await createServer({configFile:false,root:harness,plugins:[vue()],resolve:{alias:{'@':path.join(root,'src')}},server:{host:'127.0.0.1',port:0,fs:{allow:[root]}}});await server.listen()
 const origin='http://127.0.0.1:'+server.httpServer.address().port
 const {chromium}=createRequire(import.meta.url)(process.env.RUNDOCK_PLAYWRIGHT_MODULE||'playwright')
 browser=await chromium.launch({channel:'msedge',headless:true});page=await browser.newPage({viewport:{width:1440,height:950},reducedMotion:'reduce'})
 page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000)
 await page.route('**/*',async route=>{
 const req=route.request(),url=new URL(req.url());if(url.origin===origin)return route.continue();if(url.origin!=='http://wizard.fixture.invalid')return route.abort()
 const send=(body,status=200)=>route.fulfill({status,headers:{'access-control-allow-origin':'*','access-control-allow-headers':'*','access-control-allow-methods':'*'},contentType:'application/json',body:JSON.stringify(body)})
 if(req.method()==='OPTIONS')return send({})
 if(url.pathname==='/api/import/discover')return send({root:'C:/fixture',options:(mode==='single'?['A']:['A','B']).map(k=>({path:'C:/fixture/'+k+'.cmd',relativePath:k+'.cmd',kind:'script',recommended:k==='A'})),truncated:false})
 if(url.pathname==='/api/import'){
 imports++;const k=JSON.stringify(req.postDataJSON()).includes('B.cmd')?'B':'A'
 if(k==='A'&&aSlow){aArrived();await gate}
 if(k==='B'&&bFail)return send({error:'Only B failed'},500)
 return send(candidate(k))}
 if(url.pathname==='/api/apps'&&req.method()==='GET')return send([...saved].sort((a,b)=>a.sortOrder-b.sortOrder))
 if(url.pathname==='/api/apps'&&req.method()==='POST'){const body=req.postDataJSON();mutations.push(body);const app={...body,id:'created',status:'stopped'};saved.push(app);return send(app)}
 throw Error('Unexpected request '+req.method()+' '+url.pathname)
 })
 await page.goto(origin)
 await page.locator('#preview').click();await page.locator('.project-import-drop').waitFor()
 assert.match(await page.locator('.project-import-drop').evaluate(el=>getComputedStyle(el,'::before').backgroundImage),/12(?:%20| )10/)
 assert.equal(await page.locator('.drop-preview-layer').evaluate(el=>getComputedStyle(el).pointerEvents),'none')
 await page.screenshot({path:path.join(output,'drag-preview.png')});await page.locator('#preview').click()
 report.checks.push('Floating drag preview has sparse 12/10 dashes and does not intercept drop events')
 await page.locator('#open').click();await page.locator('#project-source-path').fill('C:/fixture');await page.locator('#import-app-name').waitFor()
 assert.equal(await page.locator('.entry-details').getAttribute('open'),null)
 assert.equal(await page.getByText('Only A warning',{exact:true}).isVisible(),true)
 assert.equal(await page.locator('.entry-metadata').isVisible(),false)
 await page.locator('.entry-details summary').click();assert.match(await page.locator('.entry-scope').innerText(),/18211/)
 await page.locator('.entry-details summary').click();await page.screenshot({path:path.join(output,'single-entry.png')})
 report.checks.push('Single entry details start collapsed; warnings remain visible within the same file area')
 // A new source clears old metadata and opens the multiple-entry chooser.
 mode='multiple';await page.evaluate(()=>window.receiveDrop('C:/fixture/multiple'))
 await page.locator('.startup-options input').nth(1).waitFor();await page.locator('#import-app-name').waitFor()
 assert.equal(await page.locator('.startup-details').evaluate(el=>el.open),true)
 const a=page.locator('.entry-scope[data-entry="C:/fixture/A.cmd"]'),b=page.locator('.entry-scope[data-entry="C:/fixture/B.cmd"]')
 assert.match(await a.innerText(),/Only A warning/);assert.doesNotMatch(await b.innerText(),/18211|Only A warning/)
 await b.getByRole('radio').check();await page.waitForFunction(()=>document.querySelector('#import-app-name')?.value==='Project B')
 assert.match(await b.innerText(),/18212/);assert.doesNotMatch(await a.innerText(),/18212|B-marker/)
 await page.screenshot({path:path.join(output,'multiple-entry.png')})
 report.checks.push('Multiple entries expand automatically; each command, directory, port, environment and warning stays inside its file')
 // Delayed A response must never overwrite B selection.
 aSlow=true;await a.getByRole('radio').check();await arrived
 assert.equal(await a.getByRole('status').isVisible(),true);assert.equal(await b.locator('.entry-panel').count(),0)
 await b.getByRole('radio').check();await page.waitForFunction(()=>document.querySelector('#import-app-name')?.value==='Project B');releaseSlow()
 await page.waitForTimeout(200);assert.equal(await page.locator('#import-app-name').inputValue(),'Project B');assert.doesNotMatch(await b.innerText(),/18211|Only A warning/)
 aSlow=false;bFail=true;await a.getByRole('radio').check();await page.waitForFunction(()=>document.querySelector('#import-app-name')?.value==='Project A');await b.getByRole('radio').check()
 await b.getByRole('alert').waitFor();assert.match(await b.getByRole('alert').innerText(),/Only B failed/);assert.equal(await a.getByRole('alert').count(),0)
 bFail=false;await b.getByRole('button',{name:'重试',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#import-app-name')?.value==='Project B')
 report.checks.push('Rapid entry switching rejects stale responses; entry failure and retry are scoped to B')
 await page.locator('.startup-details>summary').click();assert.equal(await page.locator('.startup-details').evaluate(el=>el.open),false)
 const before=imports;await page.locator('.startup-details>summary').click();assert.equal(imports,before)
 report.checks.push('Manually collapsing and reopening details does not repeat detection')
 await page.setViewportSize({width:390,height:600})
 await page.screenshot({path:path.join(output,'narrow.png')})
 const layout=await page.locator('.add-modal').evaluate(el=>{const r=el.getBoundingClientRect(),f=el.querySelector('footer').getBoundingClientRect();return {left:r.left,right:r.right,bottom:r.bottom,footerBottom:f.bottom,scroll:el.querySelector('.add-body').scrollHeight>el.querySelector('.add-body').clientHeight}})
 assert.ok(layout.left>=0&&layout.right<=390&&layout.bottom<=600&&layout.footerBottom<=600);assert.equal(layout.scroll,true)
 report.checks.push('Narrow viewport keeps footer visible while long entry information scrolls')
 await page.getByRole('button',{name:'确认添加',exact:true}).click();await page.getByRole('dialog').waitFor({state:'hidden'})
 assert.equal(mutations.length,1);assert.equal(mutations[0].sortOrder,-4)
 assert.equal(await page.locator('#projects li').first().innerText(),'Project B')
 await page.evaluate(()=>window.store.load());assert.equal(await page.locator('#projects li').first().innerText(),'Project B')
 report.checks.push('Creation saves a sort order before existing cards and stays first after reloading the store')
 assert.deepEqual(errors,[]);report.passed=true
}catch(e){report.error=String(e);if(page)await page.screenshot({path:path.join(output,'failure.png')}).catch(()=>{});throw e}
finally{releaseSlow();if(browser)await browser.close();if(server)await server.close();writeFileSync(path.join(output,'result.json'),JSON.stringify(report,null,2)+'\n');console.log((report.passed?'PASS':'FAIL')+': '+path.join(output,'result.json'))}

