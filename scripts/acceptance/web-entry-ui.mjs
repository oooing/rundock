// Actual AppCard in an isolated Vue fixture; never opens/stops real projects.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const root = process.cwd(), dir = path.join(root, '.tmp', 'web-entry-ui')
fs.mkdirSync(dir, { recursive: true })
fs.mkdirSync(path.join(root, 'outputs/acceptance'), { recursive: true })
fs.writeFileSync(path.join(dir, 'index.html'), '<html><body><div id="app"></div><script type="module" src="/main.js"></script></body></html>')
fs.writeFileSync(path.join(dir, 'main.js'), `import {createApp,h,reactive} from 'vue';import {createPinia} from 'pinia';import Card from '@/components/AppCard.vue';import {setLocale} from '@/i18n';import '@/styles.css';setLocale('zh-CN');window.opened=[];window.fixture=reactive({id:'fixture',name:'网页入口验证',status:'running',pid:123,lastUrl:'http://localhost:11784',entryScript:'demo/package.json',services:[{id:'ext',port:3000,role:'frontend',url:'http://localhost:3000',health:'healthy',webState:'not_page'},{id:'web',port:11784,role:'frontend',url:'http://localhost:11784',webUrl:'http://localhost:11784',webState:'available',health:'healthy'}]});createApp({render:()=>h('div',{style:'width:380px;padding:24px'},[h(Card,{app:window.fixture,groups:[],'onOpen-url':(...args)=>window.opened.push(args)})])}).use(createPinia()).mount('#app');`)
const server = await createServer({ configFile:false, root:dir, plugins:[vue()], resolve:{alias:{'@':path.join(root,'src')}}, server:{host:'127.0.0.1',port:0,fs:{allow:[root]}} })
let browser
try {
  await server.listen(); browser = await chromium.launch({channel:'msedge',headless:true})
  const page = await browser.newPage({viewport:{width:900,height:680}}), errors=[]
  page.on('pageerror',e=>errors.push(e.message)); await page.goto(server.resolvedUrls.local[0])
  await page.getByText('无网页入口',{exact:true}).waitFor()
  assert.equal(await page.locator('a[href="http://localhost:3000"]').count(),0)
  await page.locator('a[href="http://localhost:11784"]').click()
  assert.deepEqual(await page.evaluate(()=>window.opened),[['fixture','http://localhost:11784']])
  await page.screenshot({path:path.join(root,'outputs/acceptance/web-entry-card.png')})
  await page.evaluate(()=>{window.fixture.lastUrl='';window.fixture.services[1].webState='unavailable';window.fixture.services[1].webUrl=''})
  await page.getByText('网页暂不可用',{exact:true}).waitFor()
  assert.equal(await page.locator('.svc-url[href]').count(),0)
  assert.equal(await page.locator('.utility-actions > button').nth(1).isDisabled(),true)
  assert.deepEqual(errors,[])
  console.log('PASS: only verified web page is clickable; extension remains running without broken link; unavailable page disables browser entry.')
} finally { await browser?.close(); await server.close() }
