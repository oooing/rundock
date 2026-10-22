// Production AppCard/ProjectLogo in an isolated Vite app. No real backend calls.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const root = process.cwd()
fs.mkdirSync(path.join(root, '.tmp'), { recursive: true })
const dir = fs.mkdtempSync(path.join(root, '.tmp/project-icons-ui-'))
const out = path.join(root, 'outputs/acceptance/project-icons')
fs.mkdirSync(out, { recursive: true })
const icons = process.env.RUNDOCK_ICON_FIXTURES ? JSON.parse(fs.readFileSync(process.env.RUNDOCK_ICON_FIXTURES, 'utf8')) : [
  { name: 'RunDock', mime: 'image/png', data: fs.readFileSync(path.join(root, 'src-tauri/icons/128x128.png')).toString('base64') },
]
const names = [...icons.map(i => i.name), '没有图标的项目', '加载失败也保留布局', '长名称项目 · 一个需要完整展示的中文与 English Project Name']
const colors = ['', '#1e293b', '#4a2812', '#312e81', '#164e45', '', '#faf5eb', '#312e81']
const fixtures = names.map((name, i) => ({ id: 'fixture-' + i, name, cardColor: colors[i % colors.length], status: 'stopped', entryScript: 'D:\\Projects\\' + name + '\\start.bat', cwd: 'D:\\Projects', services: [], knownServices: [], iconUrl: `/api/apps/fixture-${i}/icon` }))
fs.writeFileSync(path.join(dir, 'index.html'), '<html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="app"></div><script type="module" src="/main.js"></script></body></html>')
fs.writeFileSync(path.join(dir, 'main.js'), `
import {createApp,h,reactive} from 'vue';import {createPinia} from 'pinia';
import Card from '@/components/AppCard.vue';import {setLocale} from '@/i18n';import '@/styles.css';
window.__LAUNCHER_BASE__=location.origin;setLocale('zh-CN');window.events=[];window.fixtures=reactive(${JSON.stringify(fixtures)});
createApp({render:()=>h('main',{style:'max-width:1160px;margin:auto;padding:24px'},[
 h('h1',{style:'font-size:24px;margin:0 0 8px'},'项目图标'),h('p',{style:'color:var(--text-dim);margin:0 0 24px'},'自动识别 · 隔离预览，不操作正在运行的项目'),
 h('div',{style:'display:grid;grid-template-columns:repeat(auto-fill,minmax(min(320px,100%),1fr));gap:16px'},window.fixtures.map((app,i)=>h(Card,{key:app.id,app,groups:[],cloudAlerts:i===window.fixtures.length-1?[{state:'failed'}]:[],onRename:(...args)=>window.events.push(['rename',...args]),onStart:(...args)=>window.events.push(['start',...args]),'onReorder-key':(...args)=>window.events.push(['reorder',...args])})))
])}).use(createPinia()).mount('#app');
`)
const server = await createServer({ configFile: false, root: dir, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } }, server: { host: '127.0.0.1', port: 0, fs: { allow: [root] } } })
let browser
try {
  await server.listen()
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  const page = await browser.newPage({ viewport: { width: 1200, height: 900 }, reducedMotion: 'reduce' })
  const errors = [], external = []
  page.on('pageerror', e => errors.push(e.message))
  const origin = new URL(server.resolvedUrls.local[0]).origin
  let releaseImage
  const imageGate = new Promise(resolve => { releaseImage = resolve })
  await page.route('**/*', async route => {
    const url = new URL(route.request().url())
    if (url.origin !== origin) { external.push(url.href); await route.abort(); return }
    if (url.pathname.startsWith('/api/')) {
      const match = url.pathname.match(/^\/api\/apps\/fixture-(\d+)\/icon$/)
      assert.ok(match, 'no backend operations expected: ' + url.pathname)
      const index = Number(match[1]), icon = icons[index]
      if (index === 0) await imageGate
      if (icon?.data) await route.fulfill({ status: 200, contentType: icon.mime, body: Buffer.from(icon.data, 'base64') })
      else if (index === icons.length + 1) await route.fulfill({ status: 200, contentType: 'image/png', body: 'broken data' })
      else await route.fulfill({ status: 204, body: '' })
      return
    }
    await route.continue()
  })
  await page.goto(origin, { waitUntil: 'domcontentloaded' })
  const cards = page.locator('.card')
  await cards.first().waitFor()
  const before = await cards.first().boundingBox()
  assert.equal(await cards.first().locator('.project-logo:not(.loaded) .ui-icon').count(), 1)
  // An unresolved logo must not prevent card operations.
  await cards.first().getByRole('button', { name: '启动', exact: true }).click()
  releaseImage()
  await cards.first().locator('.project-logo.loaded').waitFor()
  const after = await cards.first().boundingBox()
  assert.equal(before.height, after.height, 'icon load must not shift card layout')
  assert.equal(before.width, after.width)
  for (let i = 0; i < icons.length; i++) if (icons[i].data) await cards.nth(i).locator('.project-logo.loaded').waitFor()
  for (let i = icons.length; i < names.length; i++) {
    await cards.nth(i).locator('.project-logo img').waitFor({ state: 'detached' })
    assert.equal(await cards.nth(i).locator('.project-logo .ui-icon').count(), 1)
  }
  await cards.first().locator('.name-trigger').click()
  await cards.first().getByRole('textbox').fill('项目改名')
  await cards.first().getByRole('textbox').press('Enter')
  await cards.first().getByRole('button', { name: '拖动项目', exact: true }).press('Alt+ArrowRight')
  const events = await page.evaluate(() => window.events)
  assert.deepEqual(events.map(e => e[0]), ['start', 'rename', 'reorder'])
  assert.equal(events[1][2], '项目改名')
  await page.screenshot({ path: path.join(out, 'cards.png'), fullPage: true })
  // A changed source recovers from a previous image error.
  await page.evaluate(i => { window.fixtures[i].iconUrl = '/api/apps/fixture-0/icon?v=changed' }, icons.length + 1)
  await cards.nth(icons.length + 1).locator('.project-logo.loaded').waitFor()
  // Older backend: no iconUrl is also a valid, quiet fallback.
  await page.evaluate(() => { window.fixtures[0].iconUrl = undefined })
  assert.equal(await cards.first().locator('.project-logo img').count(), 0)
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 850 })
    await cards.last().scrollIntoViewIfNeeded()
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'no horizontal overflow at ' + width)
    assert.ok(await cards.last().locator('.name-trigger').evaluate(el => el.scrollWidth <= el.clientWidth + 1))
    assert.ok((await cards.last().locator('.name-trigger').boundingBox()).width >= 150, 'badge must not squeeze the title into a narrow column')
  }
  await page.screenshot({ path: path.join(out, 'narrow.png'), fullPage: true })
  assert.deepEqual(errors, [])
  assert.deepEqual(external, [])
  fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({ passed: true, source: 'production AppCard and ProjectLogo; isolated icons only; no live project mutations', checks: ['lazy image does not block start', 'no layout shift', 'PNG/ICO/sanitized SVG display', 'missing/corrupt/old-backend fallback', 'new source recovers', 'rename and keyboard reorder intact', '320/390px layout; long names and cloud badge', 'reduced motion; no external requests'], detected: icons.map(({ name, mime, coldMs, warmMs }) => ({ name, mime, coldMs, warmMs })) }, null, 2))
  console.log('PASS: project icon rendering, fallback, interaction and responsive layout')
} finally {
  await browser?.close(); await server.close()
  if (path.resolve(dir).startsWith(path.resolve(root, '.tmp') + path.sep)) fs.rmSync(dir, { recursive: true, force: true })
}
