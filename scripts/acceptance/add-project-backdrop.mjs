// Actual Vue wizard and native browser mouse gestures; HTTP fixtures never touch user projects.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const reproduce = process.argv.includes('--reproduce')
const evidence = path.join(root, 'outputs/acceptance/add-project-backdrop', reproduce ? 'reproduction' : 'fixed')
const harness = path.join(root, '.tmp', `add-project-backdrop-${Date.now()}`)
mkdirSync(evidence, { recursive: true }); mkdirSync(harness, { recursive: true })
const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
const fixturePath = 'C:\\fixture\\drag-selection'
const fixtureName = 'Drag Selection Fixture Project'
writeFileSync(path.join(harness, 'index.html'), '<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
writeFileSync(path.join(harness, 'main.ts'), `
import {createApp,h,ref} from 'vue'; import {createPinia} from 'pinia';
import Wizard from '${source}/components/AddProjectWizard.vue'; import '${source}/styles.css';
window.__LAUNCHER_BASE__='http://wizard.fixture.invalid'; localStorage.setItem('rundock.ui.locale','zh-CN');
createApp({setup(){const shown=ref(false);return()=>h('div',[
h('button',{id:'opener',onClick:()=>shown.value=true},'添加项目'),
shown.value?h(Wizard,{initialPath:${JSON.stringify(fixturePath)},onClose:()=>shown.value=false,onAdded:()=>shown.value=false}):null
])}}).use(createPinia()).mount('#app');
`)
const report = { passed: false, mode: reproduce ? 'reproduce original bug' : 'regression acceptance',
  boundary: 'real Vue AddProjectWizard and Edge pointer/click events; discovery/import/create HTTP responses are isolated fixtures',
  sourceSha256: createHash('sha256').update(readFileSync(path.join(root, 'src/components/AddProjectWizard.vue'))).digest('hex'), checks: [] }
let server, browser, context, page, releaseSave, saveReached
const saveGate = new Promise(resolve => { releaseSave = resolve })
const saveArrived = new Promise(resolve => { saveReached = resolve })
const pageErrors = [], mutations = []
try {
  const listener = net.createServer()
  await new Promise((resolve, reject) => { listener.once('error', reject); listener.listen(0, '127.0.0.1', resolve) })
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve))
  server = await createServer({ configFile: false, root: harness, plugins: [vue()],
    resolve: { alias: { '@': path.join(root, 'src') } }, optimizeDeps: { entries: ['index.html'] },
    server: { host: '127.0.0.1', port, strictPort: true, fs: { allow: [root] } } })
  await server.listen()
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`
  const { chromium } = createRequire(import.meta.url)(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
  browser = await chromium.launch({ channel: process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge', headless: true })
  context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1200, height: 1000 } })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror', error => pageErrors.push(error.message))
  await page.route('**/*', async route => {
    const req = route.request(), url = new URL(req.url())
    if (url.origin === origin) return route.continue()
    if (url.origin !== 'http://wizard.fixture.invalid') return route.abort()
    const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' }
    const send = (body, status = 200) => route.fulfill({ status, headers, contentType: 'application/json', body: JSON.stringify(body) })
    if (req.method() === 'OPTIONS') return send({})
    if (url.pathname === '/api/import/discover') return send({ root: fixturePath, truncated: false,
      options: [{ path: fixturePath + '\\start.cmd', relativePath: 'start.cmd', kind: 'script', recommended: true }] })
    if (url.pathname === '/api/import') return send({ name: fixtureName, entryScript: fixturePath + '\\start.cmd',
      cwd: fixturePath, projectRoot: fixturePath, adapterType: 'batch', cmd: 'cmd.exe', args: [], env: {}, portHints: [], findings: [], markers: [] })
    if (url.pathname === '/api/apps' && req.method() === 'POST') {
      mutations.push(req.postDataJSON()); saveReached(); await saveGate
      return send({ error: 'Fixture save failed; no project was created.' }, 500)
    }
    throw new Error('Unexpected fixture request: ' + req.method() + ' ' + url.pathname)
  })
  await page.goto(origin)
  await page.evaluate(() => {
    window.gestureEvents = []
    for (const type of ['pointerdown', 'pointerup', 'click']) document.addEventListener(type, event => {
      window.gestureEvents.push({ type, target: event.target.id || event.target.className || event.target.tagName })
    }, true)
  })
  const name = page.locator('#import-app-name'), dialog = page.getByRole('dialog')
  async function open() {
    await page.locator('#opener').click(); await name.waitFor()
    assert.equal(await name.inputValue(), fixtureName)
  }
  async function assertOpen() { assert.equal(await dialog.isVisible(), true, 'gesture must not dismiss the wizard') }
  async function drag(start, end) {
    await page.mouse.move(start.x, start.y); await page.mouse.down()
    await page.mouse.move(end.x, end.y, { steps: 12 }); await page.mouse.up()
  }
  async function dragOutside(input) {
    const box = await input.boundingBox()
    await page.evaluate(() => { window.gestureEvents = [] })
    await drag({ x: box.x + box.width / 2, y: box.y + box.height / 2 }, { x: 12, y: box.y + box.height / 2 })
    return page.evaluate(() => window.gestureEvents)
  }
  await open()
  await page.screenshot({ path: path.join(evidence, 'before-drag.png') })
  report.nameDragEvents = await dragOutside(name)
  if (reproduce) {
    assert.equal(await dialog.count(), 0, 'original version must reproduce the accidental dismissal')
    assert.ok(report.nameDragEvents.some(e => e.type === 'click' && e.target === 'add-overlay'))
    report.checks.push('drag starts in project-name input, ends on backdrop, and original click handler dismisses the wizard')
    await page.screenshot({ path: path.join(evidence, 'accidental-dismissal.png') })
  } else {
    await assertOpen(); assert.equal(await name.inputValue(), fixtureName)
    assert.ok(await name.evaluate(el => el.selectionEnd > el.selectionStart), 'native drag selects text')
    await page.screenshot({ path: path.join(evidence, 'name-drag-kept-open.png') })
    report.checks.push('native name selection released outside keeps the wizard and entered value')
    await dragOutside(page.locator('#project-source-path')); await assertOpen()
    assert.equal(await page.locator('#project-source-path').inputValue(), fixturePath)
    report.checks.push('source-path selection released outside also preserves the candidate')
    const box = await name.boundingBox()
    await drag({ x: 12, y: box.y + box.height / 2 }, { x: box.x + box.width / 2, y: box.y + box.height / 2 })
    await assertOpen()
    report.checks.push('a press on backdrop released inside the modal does not dismiss it')
    await drag({ x: box.x + 150, y: box.y + box.height / 2 }, { x: box.x + 20, y: box.y + box.height / 2 })
    await assertOpen(); assert.ok(await name.evaluate(el => el.selectionEnd > el.selectionStart))
    await name.fill('Edited local project')
    assert.equal(await name.inputValue(), 'Edited local project')
    report.checks.push('normal text selection and subsequent name editing remain usable')
    await page.mouse.click(12, 12); await dialog.waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => document.activeElement.id), 'opener')
    report.checks.push('genuine backdrop click closes and restores opener focus')
    for (const action of ['Escape', '关闭', '取消']) {
      await open()
      if (action === 'Escape') await name.press('Escape')
      else await page.getByRole('button', { name: action, exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
    }
    report.checks.push('Escape, close button and Cancel retain explicit dismissal')
    await open(); await name.fill('Edited local project')
    await page.getByRole('button', { name: '确认添加', exact: true }).click()
    await saveArrived; await page.mouse.click(12, 12); await assertOpen()
    releaseSave(); await page.getByRole('alert').waitFor()
    await assertOpen(); assert.equal(await name.inputValue(), 'Edited local project')
    assert.equal(mutations.length, 1); assert.equal(mutations[0].name, 'Edited local project')
    await page.mouse.click(12, 12); await dialog.waitFor({ state: 'hidden' })
    report.checks.push('saving still blocks backdrop dismissal; failed save retains edits and permits later dismissal')
  }
  assert.deepEqual(pageErrors, [])
  report.checks.push('no browser runtime error or real project mutation')
  report.passed = true
} catch (error) {
  report.error = String(error)
  if (page) await page.screenshot({ path: path.join(evidence, 'failure.png') }).catch(() => {})
  throw error
} finally {
  releaseSave()
  if (context) await context.tracing.stop({ path: path.join(evidence, 'browser-trace.zip') }).catch(() => {})
  if (browser) await browser.close()
  if (server) await server.close()
  writeFileSync(path.join(evidence, 'result.json'), JSON.stringify(report, null, 2) + '\n')
  console.log(`${report.passed ? 'PASS' : 'FAIL'}: ${path.join(evidence, 'result.json')}`)
}
