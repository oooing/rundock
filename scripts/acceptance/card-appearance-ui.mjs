// Real Dashboard + SettingsModal, isolated Vite server and intercepted API only.
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
const dir = fs.mkdtempSync(path.join(root, '.tmp/card-appearance-ui-'))
const out = path.join(root, 'outputs/acceptance/card-appearance')
fs.mkdirSync(out, { recursive: true })
fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({ passed: false, status: 'running' }))
const icons = ['localplay.png', 'duck.png', 'rundock.png'].map(name => fs.readFileSync(path.join(root, 'design/project-icons/assets', name)))
const names = ['localPlay', '看剧鸭', 'RunDock', '没有图标的项目', '图标加载失败', '长名称项目 · 一个完整的中文与 English Project Name']
const fixtures = names.map((name, i) => ({
  id: 'fixture-' + i, name, status: i === 1 ? 'failed' : i === 0 ? 'running' : 'stopped',
  cardColor: ['#1e293b', '#312e81', '#faf5eb', '#164e45', '#4a2812', ''][i],
  entryScript: 'D:\\Projects\\' + name + '\\start.bat', cwd: 'D:\\Projects',
  services: [9100, 8009].map((port, n) => ({ id: String(port), port, role: n ? 'backend' : 'frontend', url: 'http://localhost:' + port, health: 'healthy', browserEntry: true })),
  knownServices: [], iconUrl: '/api/apps/fixture-' + i + '/icon',
}))
fs.writeFileSync(path.join(dir, 'index.html'), '<html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="app"></div><script type="module" src="/main.js"></script></body></html>')
fs.writeFileSync(path.join(dir, 'main.js'), `
import {createApp,h,ref} from 'vue';import {createPinia} from 'pinia';
import Dashboard from '@/views/Dashboard.vue';import Settings from '@/components/SettingsModal.vue';import {setLocale} from '@/i18n';import '@/styles.css';
window.__LAUNCHER_BASE__=location.origin;setLocale('zh-CN');window.events=[];
const apps=ref(${JSON.stringify(fixtures)}), settings=ref(false);window.fixtures=apps.value;
createApp({render:()=>h('main',{style:'max-width:1160px;margin:auto;padding:20px'},[
 h('header',{style:'display:flex;align-items:center;justify-content:space-between;margin-bottom:20px'},[h('h1',{style:'font-size:24px;margin:0'},'正式卡片 · 隔离验证'),h('button',{onClick:()=>settings.value=true},'设置')]),
 h(Dashboard,{apps:apps.value,loading:false,ready:true,groups:[{id:'dev',name:'开发'}],moving:{},cloudAlerts:[{appId:'fixture-5',state:'failed'}],
 onStart:id=>window.events.push(['start',id]),onStop:id=>window.events.push(['stop',id]),onRelease:id=>window.events.push(['release',id]),
 onReorder:order=>{window.events.push(['reorder',order]);apps.value=order.map(id=>apps.value.find(a=>a.id===id))}}),
 settings.value?h(Settings,{onClose:()=>settings.value=false}):null
])}).use(createPinia()).mount('#app');
`)
const server = await createServer({ configFile: false, root: dir, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } }, server: { host: '127.0.0.1', port: 0, fs: { allow: [root] } } })
let browser
const checks = []
try {
  await server.listen()
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  const page = await browser.newPage({ viewport: { width: 1200, height: 950 }, reducedMotion: 'reduce' })
  page.setDefaultTimeout(10000)
  const errors = [], unexpected = [], apiRequests = []
  page.on('pageerror', e => errors.push(e.message))
  const origin = new URL(server.resolvedUrls.local[0]).origin
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin) { unexpected.push(url.href); return route.abort() }
    if (!url.pathname.startsWith('/api/')) return route.continue()
    apiRequests.push([request.method(), url.pathname])
    if (request.method() !== 'GET') { unexpected.push(request.method() + ' ' + url.pathname); return route.abort() }
    if (url.pathname === '/api/settings') return route.fulfill({ json: { grace_period_seconds: '8' } })
    if (url.pathname.endsWith('/startup-issue')) return route.fulfill({ json: null })
    const match = url.pathname.match(/^\/api\/apps\/fixture-(\d+)\/icon$/)
    if (!match) { unexpected.push(url.pathname); return route.abort() }
    const index = Number(match[1])
    if (icons[index]) return route.fulfill({ contentType: 'image/png', body: icons[index] })
    if (index === 4) return route.fulfill({ contentType: 'image/png', body: 'broken image' })
    return route.fulfill({ status: 204, body: '' })
  })
  await page.goto(origin, { waitUntil: 'networkidle' })
  const card = id => page.locator(`[data-card-id="fixture-${id}"] .card`)
  const openSettings = () => page.getByRole('button', { name: '设置', exact: true }).click()
  const closeSettings = () => page.getByRole('dialog', { name: '设置', exact: true }).getByRole('button', { name: '关闭', exact: true }).click()
  const setChoice = async (name, value) => {
    await openSettings()
    await page.locator(`input[name="${name}"][value="${value}"]`).check()
    await closeSettings()
  }
  await card(0).locator('.project-logo.loaded').waitFor()
  assert.equal(await page.locator('.card.design-icon.handle-top-center').count(), fixtures.length)
  assert.equal(await card(1).evaluate(el => getComputedStyle(el).getPropertyValue('--card-state-color').trim()), '#ef4444')
  assert.equal(await card(0).locator('.services-heading').innerText(), '服务地址')
  assert.equal(await card(0).locator('.services-heading *').count(), 0)
  checks.push('default title icon/top-center; failed stripe red; service heading has no count')
  await page.screenshot({ path: path.join(out, 'title-icons.png'), fullPage: true })

  await openSettings()
  assert.equal(await page.locator('input[name="card-design"]').count(), 2)
  assert.equal(await page.locator('input[name="card-handle-position"]').count(), 2)
  await page.locator('input[name="card-design"][value="icon"]').focus()
  await page.keyboard.press('ArrowRight')
  assert.ok(await page.locator('input[name="card-design"][value="watermark"]').isChecked())
  assert.equal(await page.locator('.card.design-watermark').count(), fixtures.length)
  await page.locator('input[name="card-handle-position"][value="top-left"]').check()
  await page.locator('.card-appearance-settings').screenshot({ path: path.join(out, 'settings.png') })
  await closeSettings()
  await card(0).locator('.project-logo-watermark.loaded').waitFor()
  await card(3).locator('.project-logo').waitFor({ state: 'detached' })
  await card(4).locator('.project-logo').waitFor({ state: 'detached' })
  assert.equal(await card(0).locator('.name-row .project-logo').count(), 0)
  assert.equal(await card(0).locator('.project-decoration').evaluate(el => getComputedStyle(el).pointerEvents), 'none')
  assert.equal(await card(0).locator('.project-logo-watermark').evaluate(el => getComputedStyle(el).opacity), '0.12')
  await page.screenshot({ path: path.join(out, 'watermarks.png'), fullPage: true })
  checks.push('real SettingsModal; two choices each; keyboard radio control; immediate style change; missing/corrupt watermark hidden')

  await page.reload({ waitUntil: 'networkidle' })
  assert.equal(await page.locator('.card.design-watermark.handle-top-left').count(), fixtures.length)
  await page.evaluate(() => { window.fixtures[4].iconUrl = '/api/apps/fixture-2/icon?recovered' })
  await card(4).locator('.project-logo-watermark.loaded').waitFor()
  await card(0).getByRole('button', { name: '停止', exact: true }).click()
  await card(2).getByRole('button', { name: '启动', exact: true }).click()
  await card(2).getByRole('button', { name: '发布', exact: true }).click()
  assert.deepEqual(await page.evaluate(() => window.events.map(e => e[0])), ['stop', 'start', 'release'])
  checks.push('settings survive reload; watermark source recovery; start/stop/release emit intact (no real actions)')

  for (const position of ['top-left', 'top-center']) {
    await setChoice('card-handle-position', position)
    for (const design of ['watermark', 'icon']) {
    await setChoice('card-design', design)
    const handle = card(0).getByRole('button', { name: '拖动项目', exact: true })
    const box = await handle.boundingBox(), parent = await card(0).boundingBox()
    const vertical = position === 'top-left'
    assert.equal(box.width, vertical ? 24 : 28); assert.equal(box.height, vertical ? 28 : 24)
    if (vertical) {
      const next = await card(0).locator(design === 'icon' ? '.name-row .project-logo' : '.name-trigger').boundingBox()
      assert.ok(next.x - box.x - box.width >= 6 && next.x - box.x - box.width <= 10, 'grip is immediately left of logo/title')
      assert.ok(Math.abs(box.y + box.height / 2 - next.y - next.height / 2) < 1, 'grip aligns with logo/title center')
      assert.equal(await card(0).evaluate(el => getComputedStyle(el).paddingTop), '16px', 'inline grip adds no top strip')
    } else {
      assert.ok(Math.abs(box.x + 14 - parent.x - parent.width / 2) < 2 && box.y - parent.y <= 4)
    }
    assert.deepEqual(await handle.locator('circle').evaluateAll(els => [els.length, new Set(els.map(el => el.getAttribute('cx'))).size, new Set(els.map(el => el.getAttribute('cy'))).size]), vertical ? [3, 1, 3] : [3, 3, 1])
    await page.mouse.move(0, 0)
    const plain = await handle.evaluate(el => getComputedStyle(el).backgroundColor)
    await handle.hover()
    const hover = await handle.evaluate(el => ({ background: getComputedStyle(el).backgroundColor, shadow: getComputedStyle(el).boxShadow }))
    assert.notEqual(hover.background, plain); assert.equal(hover.shadow, 'none')
    await card(0).screenshot({ path: path.join(out, 'hover-' + position + '-' + design + '.png') })
    await handle.press('Tab'); await page.keyboard.press('Shift+Tab')
    assert.equal(await handle.evaluate(el => getComputedStyle(el).outlineStyle), 'solid')
    const before = await page.locator('.card-slot').evaluateAll(els => els.map(el => el.dataset.cardId))
    const firstHandle = page.locator('.card-slot').first().getByRole('button', { name: '拖动项目', exact: true })
    const from = await firstHandle.boundingBox(), to = await page.locator('.card-slot').nth(1).boundingBox()
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2)
    await page.mouse.down()
    await page.mouse.move(to.x + to.width - 20, to.y + 40, { steps: 12 })
    await page.locator('.card-drag-preview').waitFor()
    await page.mouse.up()
    await page.waitForFunction(id => document.querySelector('.card-slot')?.dataset.cardId !== id, before[0])
    await page.locator('.card-slot').first().getByRole('button', { name: '拖动项目', exact: true }).press('Alt+ArrowRight')
    await page.waitForFunction(id => document.querySelector('.card-slot')?.dataset.cardId === id, before[0])
    }
  }
  checks.push('vertical grip left of logo/title and horizontal top-center grip: compact alignment, hover, focus, real pointer drag and Alt-arrow reorder in both icon styles')
  // Decoration is clipped separately; menus remain usable above card contents.
  await card(0).locator('.role-btn').first().click()
  const roleButton = card(0).locator('.role-menu button').first()
  assert.ok(await roleButton.evaluate(el => { const r = el.getBoundingClientRect(); return el.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2)) }))
  await page.keyboard.press('Escape')
  await card(0).getByLabel('更多操作', { exact: true }).click()
  await card(0).getByRole('button', { name: '改名', exact: true }).click()
  await card(0).getByRole('textbox').press('Escape')
  checks.push('service menu and more-actions remain accessible')

  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 850 })
    for (const design of ['icon', 'watermark']) {
      await setChoice('card-design', design)
      for (const position of ['top-center', 'top-left']) {
      await setChoice('card-handle-position', position)
      await card(5).scrollIntoViewIfNeeded()
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `no overflow: ${width} ${design}`)
      assert.ok((await card(5).locator('.name-trigger').boundingBox()).width >= 150)
      }
    }
    await openSettings()
    assert.ok(await page.locator('.m-body').evaluate(el => el.scrollWidth <= el.clientWidth + 1), 'settings no overflow at ' + width)
    await page.locator('.card-appearance-settings').scrollIntoViewIfNeeded()
    await page.screenshot({ path: path.join(out, 'settings-' + width + '.png') })
    await closeSettings()
  }
  await page.screenshot({ path: path.join(out, 'narrow.png'), fullPage: true })
  await page.setViewportSize({ width: 1200, height: 950 })
  await openSettings()
  await page.locator('#ui-language').selectOption('en')
  assert.equal(await page.getByRole('heading', { name: 'Card appearance', exact: true }).count(), 1)
  assert.ok(await page.getByRole('radio', { name: /^Corner watermark/ }).isChecked())
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
  await page.emulateMedia({ forcedColors: 'active' })
  assert.equal(await card(0).locator('.project-logo-watermark').evaluate(el => getComputedStyle(el).display), 'none')
  checks.push('320/390px cards and settings; long names; English; reduced motion; watermark omitted in forced colors')
  assert.deepEqual(errors, []); assert.deepEqual(unexpected, [])
  fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({ passed: true, checks, apiRequests, source: 'production Dashboard, AppCard and SettingsModal; isolated mock API only' }, null, 2))
  console.log('PASS: ' + checks.join('\nPASS: '))
} finally {
  await browser?.close(); await server.close()
  if (path.resolve(dir).startsWith(path.resolve(root, '.tmp') + path.sep)) fs.rmSync(dir, { recursive: true, force: true })
}
