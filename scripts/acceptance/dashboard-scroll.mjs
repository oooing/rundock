// Real application shell, isolated HTTP/WS; no user projects or services are touched.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const evidence = path.join(root, 'outputs/acceptance/dashboard-scroll')
const fixture = path.join(root, '.tmp/dashboard-scroll')
await mkdir(evidence, { recursive: true }); await mkdir(fixture, { recursive: true })
const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
await writeFile(path.join(fixture, 'index.html'), '<html lang="zh"><head><meta charset="UTF-8"></head><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
await writeFile(path.join(fixture, 'main.ts'), `import {createApp} from 'vue'; import {createPinia} from 'pinia'; import App from '${source}/App.vue'; import '${source}/styles.css'; import {setLocale} from '${source}/i18n/index.ts'; setLocale('zh-CN'); window.setTestLocale=setLocale; window.__LAUNCHER_BASE__='http://fixture.invalid'; createApp(App).use(createPinia()).mount('#app');`)
const server = await createServer({configFile:false,root:fixture,plugins:[vue()],resolve:{alias:{'@':path.join(root,'src')}},optimizeDeps:{entries:['index.html']},server:{host:'127.0.0.1',port:19485,strictPort:false,fs:{allow:[root]}}})
let browser, page
const report = {passed:false, boundary:'real App shell; simulated HTTP and blocked WebSockets', checks:[]}
try {
  await server.listen()
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`
  browser = await chromium.launch({channel:process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge',headless:true})
  page = await browser.newPage({viewport:{width:1674,height:956}})
  const errors = [], unexpected = []
  page.on('pageerror', e=>errors.push(e.message))
  const apps = Array.from({length:24},(_,i)=>({id:`fixture-${i}`,name:`测试项目 ${i+1}`,entryScript:'C:/fixture/start.cmd',cwd:'C:/fixture',args:[],env:{},tags:[],services:[],portHints:[],status:'stopped',groupId:'dev',adapterType:'batch',cardColor:i%2?'#382569':'#72330d'}))
  await page.routeWebSocket('**/*', ws=>ws.close())
  await page.route('**/*', async route=>{
    const req = route.request(), url = new URL(req.url())
    if(url.origin===origin && !url.pathname.startsWith('/api/')) return route.continue()
    if(url.origin==='http://fixture.invalid' && ['GET','OPTIONS'].includes(req.method())) {
      const body = req.method()==='OPTIONS'?{}: url.pathname==='/api/health'?{apiVersion:'2',capabilities:'release-v2'}:url.pathname==='/api/apps'?apps:url.pathname==='/api/groups'?[{id:'dev',name:'DEV',color:''}]:url.pathname==='/api/cloud-builds'?[]:{}
      return route.fulfill({status:200,headers:{'access-control-allow-origin':'*','access-control-allow-headers':'*'},contentType:'application/json',body:JSON.stringify(body)})
    }
    unexpected.push(`${req.method()} ${req.url()}`); await route.abort()
  })
  await page.goto(origin)
  await page.locator('article.card').nth(23).waitFor({state:'attached'})
  const content = page.locator('.main > .content'), header = page.locator('.topbar')
  async function checkScroll(label) {
    await content.evaluate(el=>{el.scrollTop=0})
    const top = await header.boundingBox(), summary = await page.locator('.workspace-summary').boundingBox()
    const area = await content.boundingBox()
    await content.evaluate(el=>{el.scrollTop=160})
    await page.screenshot({path:path.join(evidence,`${label}.png`)})
    assert.ok(area.y >= top.y+top.height-.5, `${label}: scroll area overlaps header by ${top.y+top.height-area.y}px`)
    assert.ok(summary.y+summary.height <= area.y, 'summary stays outside the scroll viewport')
    assert.deepEqual(await header.boundingBox(),top,'header stays fixed')
    assert.ok(await content.evaluate(el=>el.scrollTop>0),'cards actually scroll')
    assert.ok(await page.locator('.workspace-summary').evaluate(el=>{
      const r=el.getBoundingClientRect()
      return document.elementsFromPoint(r.x+10,r.y+r.height/2).every(n=>!n.closest('article.card'))
    }),'no card is painted/hit-tested behind summary text')
    await content.evaluate(el=>{el.scrollTop=el.scrollHeight})
    await page.locator('article.card').last().getByRole('button',{name:/^(启动|Start)$/}).focus()
    assert.equal(await page.evaluate(()=>window.scrollY),0)
    await content.evaluate(el=>{el.scrollTop=0})
    assert.ok((await page.locator('article.card').first().boundingBox()).y>=area.y-.5)
    report.checks.push(`${label}: disjoint header and scroller, fixed title, accessible last card, no page scrolling`)
  }
  await checkScroll('desktop')
  await page.setViewportSize({width:800,height:560})
  await checkScroll('small-window')
  await page.setViewportSize({width:560,height:700})
  await checkScroll('wrapped-header')
  await page.setViewportSize({width:1674,height:956})
  await page.locator('.sidebar').getByTitle('DEV',{exact:true}).click()
  await checkScroll('group')
  await page.getByRole('button',{name:'DEV',exact:true}).last().click()
  await page.getByRole('textbox',{name:'分组名称'}).press('Escape')
  await page.getByLabel('管理分组').click()
  assert.ok(await page.getByRole('button',{name:'删除分组',exact:true}).isVisible(),'group menu is not clipped by header')
  await page.keyboard.press('Escape')
  await page.evaluate(()=>window.setTestLocale('en'))
  await checkScroll('english')
  assert.deepEqual(errors,[])
  assert.deepEqual(unexpected,[])
  report.passed=true
} catch(error) {
  report.error=String(error)
  throw error
} finally {
  await writeFile(path.join(evidence,'report.json'),JSON.stringify(report,null,2))
  await browser?.close(); await server.close()
}
console.log(JSON.stringify(report,null,2))
