import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const { chromium } = createRequire(import.meta.url)(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const dir = path.join(root, '.tmp/release-file-review-ui')
const evidence = path.join(root, 'outputs/release-file-review-ui')
await mkdir(dir, { recursive: true })
await mkdir(evidence, { recursive: true })
const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
await writeFile(path.join(dir, 'index.html'), '<html lang="zh"><head><meta charset="utf-8"></head><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
await writeFile(path.join(dir, 'main.ts'), [
  "import {createApp,h,ref} from 'vue';",
  "import Panel from '" + source + "/components/ReleaseSafetyPanel.vue';",
  "import {setLocale} from '" + source + "/i18n/index.ts';",
  "import '" + source + "/styles.css';setLocale('zh-CN');",
  "const scope={id:'rundock:scope',name:'文件范围与版本',required:true,status:'blocked',reason:'仍有文件用途需要确认，请在文件范围中处理。'};",
  "const file={path:'notes/unknown.txt',category:'review',contentFingerprint:'fixture',reasons:[]};",
  "createApp({setup(){const mode=ref('blocked'),decisions=ref([]);return()=>h('main',{style:'max-width:850px;margin:24px auto;padding:16px'},[",
  "h('h2','文件确认 · 隔离验收'),...['blocked','legacy','failed','passed'].map(m=>h('button',{onClick:()=>{mode.value=m;decisions.value=[]}},m)),",
  "h(Panel,{appId:'fixture',intent:'formal',files:mode.value==='passed'?[]:[file],selected:{},decisions:decisions.value,busy:false,stale:decisions.value.length>0,",
  "candidate:{id:'fixture',intent:'formal',status:mode.value==='legacy'?'failed':mode.value,accepted:mode.value==='passed',sensitiveFindings:[],dependencyFindings:[],warnings:[],",
  "checkResults:mode.value==='passed'?[]:[{...scope,status:mode.value==='legacy'?'failed':'blocked'},...(mode.value==='failed'?[{id:'test',name:'测试',required:true,status:'failed',reason:'检查命令失败',log:'AssertionError'}]:[])]},",
  "onChoose:(f,included)=>decisions.value=[{path:f.path,contentFingerprint:f.contentFingerprint,decision:included?'include':'exclude'}]})])}}).mount('#app');",
].join('\n'))
const server = await createServer({ configFile:false, root:dir, plugins:[vue()], resolve:{alias:{'@':path.join(root,'src')}}, server:{host:'127.0.0.1',port:19484,strictPort:false,fs:{allow:[root]}} })
let browser
try {
  await server.listen()
  const origin = 'http://127.0.0.1:' + server.httpServer.address().port
  browser = await chromium.launch({channel:'msedge',headless:true})
  const page = await browser.newPage({viewport:{width:1100,height:850}})
  const errors=[]
  page.on('pageerror',error=>errors.push(error.message))
  // Never contact the live backend or any external endpoint.
  await page.route('**/*',route=>route.request().url().startsWith(origin+'/') ? route.continue() : route.abort())
  await page.goto(origin)
  const status = page.locator('.candidate-result .safety-head > span')
  await page.locator('.check-review').waitFor()
  for(const mode of ['blocked','legacy']){
    await page.getByRole('button',{name:mode,exact:true}).click()
    assert.equal(await status.innerText(),'待确认文件')
    assert.equal(await status.getAttribute('class'),'review')
    assert.equal(await status.evaluate(el=>getComputedStyle(el).color),await page.locator('.check-review strong').evaluate(el=>getComputedStyle(el).color))
    assert.equal(await page.locator('.check-failure').count(),0)
    assert.equal(await page.getByRole('button',{name:'重新检查',exact:true}).count(),0)
    await page.getByRole('button',{name:'确认文件用途',exact:true}).click()
    assert.equal(await page.locator('select').evaluate(el=>el===document.activeElement),true)
    await page.screenshot({path:path.join(evidence,mode+'.png'),fullPage:true})
  }
  await page.locator('select').selectOption('exclude')
  assert.equal(await status.innerText(),'需重新检查')
  assert.equal(await page.getByRole('button',{name:'重新检查',exact:true}).count(),1)
  await page.getByRole('button',{name:'failed',exact:true}).click()
  assert.equal(await status.innerText(),'检查失败')
  assert.equal(await page.locator('.check-failure').count(),1)
  await page.getByRole('button',{name:'passed',exact:true}).click()
  assert.equal(await status.innerText(),'检查通过')
  await page.getByRole('button',{name:'blocked',exact:true}).click()
  await page.setViewportSize({width:560,height:850})
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
  await page.screenshot({path:path.join(evidence,'narrow.png'),fullPage:true})
  assert.deepEqual(errors,[])
  console.log('PASS: review/legacy/real failure/passed/stale/focus/narrow; no live API')
} finally {
  await browser?.close()
  await server.close()
}
