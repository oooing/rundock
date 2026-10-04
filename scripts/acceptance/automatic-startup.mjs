// Real Vue + isolated HTTP/SQLite import acceptance. No project entry is executed.
// Reuse a current sidecar/.tmp/port-resolution-validation.exe or set RUNDOCK_SIDECAR.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs'
import { createRequire } from 'node:module'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const dir = path.join(root, 'sidecar/.tmp', `automatic-startup-${Date.now()}`)
const evidence = path.join(root, 'outputs/acceptance', path.basename(dir))
mkdirSync(evidence, { recursive: true })
const report = { passed: false, boundary: 'actual Vue wizard, actual discovery/import/create API and isolated SQLite; transport delays only for races', checks: [] }
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
async function until(check, timeout = 20000) {
  const end = Date.now() + timeout
  while (Date.now() < end) { if (await check()) return; await pause(100) }
  throw new Error('Timed out waiting for acceptance state')
}
async function freePort() {
  const server = net.createServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}
const apiPort = await freePort(), uiPort = await freePort()
const base = `http://127.0.0.1:${apiPort}`, uiBase = `http://127.0.0.1:${uiPort}`
async function request(route, method = 'GET', body) {
  const response = await fetch(base + route, { method, headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(25000) })
  const value = await response.json()
  assert.ok(response.ok, `${method} ${route}: ${JSON.stringify(value)}`)
  return value
}
function fixture(name, scripts = [], withPackage = false) {
  const folder = path.join(dir, 'projects', name)
  mkdirSync(folder, { recursive: true })
  // This marker makes accidental execution observable, without starting any listener.
  writeFileSync(path.join(folder, 'sentinel.cjs'), "require('node:fs').writeFileSync(__dirname+'/executed.txt','unexpected execution')\n")
  for (const script of scripts) writeFileSync(path.join(folder, script), `@echo off\r\n"${process.execPath}" "%~dp0sentinel.cjs"\r\n`)
  if (withPackage) writeFileSync(path.join(folder, 'package.json'), JSON.stringify({ name, packageManager: 'npm@10.0.0', scripts: { dev: 'node sentinel.cjs' } }, null, 2))
  return folder
}
const scriptProject = fixture('script-and-npm', ['start.cmd'], true)
const packageProject = fixture('package-only', [], true)
const ambiguousProject = fixture('ambiguous', ['alpha.cmd', 'beta.cmd'])
const emptyProject = fixture('empty')
const allProjects = [scriptProject, packageProject, ambiguousProject, emptyProject]
const originalFiles = allProjects.flatMap(folder => ['start.cmd', 'alpha.cmd', 'beta.cmd', 'sentinel.cjs', 'package.json']
  .map(file => path.join(folder, file)).filter(existsSync).map(file => [file, readFileSync(file, 'utf8')]))
let backend, vite, browser, context, page
const gates = []
try {
  const harness = path.join(dir, 'ui'); mkdirSync(harness)
  const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
  writeFileSync(path.join(harness, 'index.html'), '<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
  writeFileSync(path.join(harness, 'main.ts'), `import {createApp,h,ref,nextTick} from 'vue';import {createPinia} from 'pinia';\n` +
    `import Wizard from '${source}/components/AddProjectWizard.vue';import {useAppsStore} from '${source}/stores/apps.ts';import '${source}/styles.css';\n` +
    `window.__LAUNCHER_BASE__=${JSON.stringify(base)};\n` +
    `createApp({setup(){const store=useAppsStore(),shown=ref(false),initial=ref(''),key=ref(0),wizard=ref();\n` +
    `window.openFixtureWizard=async p=>{await store.load();initial.value=p||'';key.value++;shown.value=true;await nextTick()};\n` +
    `window.fixtureDrop=paths=>wizard.value.receiveDrop(paths);window.fixtureAdded=[];\n` +
    `return()=>h('div',[h('button',{id:'fixture-opener',onClick:()=>window.openFixtureWizard('')},'Open fixture'),shown.value?h(Wizard,{ref:wizard,key:key.value,initialPath:initial.value,onClose:()=>shown.value=false,onAdded:n=>{window.fixtureAdded.push(n);shown.value=false}}):h('p','Acceptance ready')])}}).use(createPinia()).mount('#app');\n`)
  vite = await createServer({ configFile: false, root: harness, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } },
    optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: uiPort, strictPort: true, fs: { allow: [root] } } })
  await vite.listen()
  const executable = process.env.RUNDOCK_SIDECAR || path.join(root, 'sidecar/.tmp/port-resolution-validation.exe')
  assert.ok(existsSync(executable), 'Build the acceptance sidecar first')
  backend = spawn(executable, ['-port', String(apiPort)], { windowsHide: true, cwd: dir,
    env: { ...process.env, LAUNCHER_DATA_DIR: path.join(dir, 'data'), LAUNCHER_UI_ORIGIN: uiBase }, stdio: 'ignore' })
  await until(async () => { try { return (await request('/api/health')).status === 'ok' } catch { return false } })
  const require = createRequire(import.meta.url)
  const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
  browser = await chromium.launch({ channel: process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge', headless: true })
  context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1150, height: 950 } })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage()
  const pageErrors = [], calls = []
  page.on('pageerror', error => pageErrors.push(error.message))
  page.on('request', req => { if (req.url().startsWith(base) && req.method() === 'POST') calls.push({ url: req.url().slice(base.length), body: req.postDataJSON() }) })
  await page.goto(uiBase)
  await page.waitForFunction(() => typeof window.openFixtureWizard === 'function')
  const input = page.locator('#project-source-path'), primary = page.locator('.add-modal footer button.primary')
  async function open(initial = '') { await page.locator('#fixture-opener').focus(); await page.evaluate(p => window.openFixtureWizard(p), initial); await input.waitFor() }
  async function ready() { await page.locator('#import-app-name').waitFor(); await until(() => primary.isEnabled()) }
  async function close() {
    await page.getByRole('button', { name: '关闭', exact: true }).click()
    await page.getByRole('dialog').waitFor({ state: 'hidden' })
    assert.equal(await page.evaluate(() => document.activeElement?.id), 'fixture-opener', 'close restores the opener focus')
  }
  async function advanced() { const details = page.locator('details.advanced'); if (!await details.evaluate(el => el.open)) await details.locator('summary').click() }
  async function heldResponse(routePattern, matches) {
    let release, reached
    const gate = new Promise(resolve => { release = resolve })
    const arrived = new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('Delayed request never arrived')), 20000)
      reached = () => { clearTimeout(timer); resolve() }
    })
    gates.push(release)
    const handler = async route => {
      if (!matches(route.request().postDataJSON())) return route.continue()
      const response = await route.fetch()
      reached(); await gate
      try { await route.fulfill({ response }) } catch { /* An intentionally aborted old request is expected. */ }
    }
    await page.route(routePattern, handler)
    return { arrived, release, clear: () => page.unroute(routePattern, handler) }
  }

  await open()
  await input.fill(`"${scriptProject}"`)
  await ready()
  assert.equal(await page.getByRole('button', { name: '识别启动方式', exact: true }).count(), 0)
  assert.equal(await page.locator('.startup-options').isVisible(), false)
  await page.getByText('start.cmd', { exact: false }).first().waitFor()
  assert.equal(await page.locator('details.advanced').evaluate(el => el.open), false)
  await page.screenshot({ path: path.join(evidence, 'automatic-compact.png') })
  report.checks.push('quoted pasted folder automatically selects start.cmd over npm; normal view hides manual detection and alternate choices')
  await advanced()
  await page.getByRole('radio').nth(1).check()
  await ready()
  assert.ok(await page.locator('details.advanced').innerText().then(text => text.includes('npm')))
  await page.screenshot({ path: path.join(evidence, 'alternate-entry.png') })
  await close()
  assert.equal((await request('/api/apps')).length, 0)
  report.checks.push('advanced settings retain a selectable npm alternative; closing does not add anything')

  await open(packageProject); await ready()
  assert.equal(await page.evaluate(() => document.activeElement?.id), 'project-source-path')
  await input.press('Shift+Tab')
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('aria-label')), '关闭')
  await page.getByRole('button', { name: '关闭', exact: true }).press('Shift+Tab')
  assert.equal(await page.evaluate(() => document.activeElement?.classList.contains('primary')), true)
  await primary.press('Tab')
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute('aria-label')), '关闭')
  report.checks.push('initial-path detection focuses the dialog; forward and reverse Tab remain within it')
  assert.equal(await page.getByRole('radio').count(), 0)
  await page.getByText('npm run dev', { exact: false }).first().waitFor()
  await close()
  await open(); await page.evaluate(p => window.fixtureDrop([p]), scriptProject); await ready(); await close()
  report.checks.push('package-only and initial/native-drop paths automatically resolve without running scripts')

  await open(ambiguousProject)
  await page.getByText('高级设置', { exact: true }).waitFor()
  assert.equal(await primary.isEnabled(), false)
  assert.equal(await page.locator('#import-app-name').count(), 0)
  await advanced(); await page.getByRole('radio').first().check(); await ready(); await close()
  report.checks.push('ambiguous low-confidence entries are not arbitrarily selected; advanced selection remains usable')
  await open(emptyProject)
  await page.getByText('暂未识别到启动方式', { exact: true }).waitFor()
  assert.equal(await primary.isEnabled(), false)
  writeFileSync(path.join(emptyProject, 'start.cmd'), `@echo off\r\n"${process.execPath}" "%~dp0sentinel.cjs"\r\n`)
  await page.getByRole('button', { name: '重新识别', exact: true }).click()
  await ready(); await close()
  report.checks.push('adding an entry after an empty scan can be retried on the same folder without editing its path')
  await open(path.join(dir, 'missing'))
  await page.getByRole('alert').waitFor()
  assert.equal(await primary.isEnabled(), false)
  await page.getByRole('button', { name: /重试|重新识别/, exact: false }).waitFor()
  await input.fill(packageProject); await ready(); await close()
  report.checks.push('empty/error states show next steps; editing a failed path automatically recovers')

  const staleDiscovery = await heldResponse('**/api/import/discover', body => body.path === scriptProject)
  await open(scriptProject); await staleDiscovery.arrived
  assert.equal(await input.isEnabled(), true); assert.equal(await primary.isEnabled(), false)
  await input.fill(packageProject); await ready()
  assert.equal(await page.locator('#import-app-name').inputValue(), 'package-only')
  staleDiscovery.release(); await pause(350)
  assert.equal(await page.locator('#import-app-name').inputValue(), 'package-only')
  await staleDiscovery.clear(); await close()
  report.checks.push('editing during slow discovery cancels old work and never restores a stale candidate')

  const staleImport = await heldResponse('**/api/import', body => body.scriptPath === path.join(scriptProject, 'start.cmd'))
  await open(scriptProject); await staleImport.arrived
  await advanced(); await page.getByRole('radio').nth(1).check(); await ready()
  await page.locator('#import-app-name').fill('latest-selected-entry')
  staleImport.release(); await pause(350)
  assert.equal(await page.locator('#import-app-name').inputValue(), 'latest-selected-entry')
  await staleImport.clear(); await close()
  report.checks.push('changing the entry during slow import rejects its late candidate response')

  const closePending = await heldResponse('**/api/import/discover', body => body.path === ambiguousProject)
  await open(ambiguousProject); await closePending.arrived; await close()
  closePending.release(); await pause(200); await closePending.clear()
  await open(packageProject); await ready(); await close()
  report.checks.push('close during recognition cancels read-only work; reopening has clean state')

  await open(scriptProject); await ready()
  await page.locator('#import-app-name').fill('automatic-added')
  const createBefore = calls.filter(call => call.url === '/api/apps').length
  await primary.evaluate(button => { button.click(); button.click() })
  await page.getByRole('dialog').waitFor({ state: 'hidden' })
  const created = await request('/api/apps')
  assert.equal(created.length, 1)
  assert.equal(created[0].entryScript.toLowerCase(), path.join(scriptProject, 'start.cmd').toLowerCase())
  assert.equal(created[0].name, 'automatic-added')
  assert.equal(calls.filter(call => call.url === '/api/apps').length - createBefore, 1)
  await open(scriptProject)
  await page.getByText('这个启动入口已经添加，可直接使用现有项目卡片。', { exact: true }).waitFor()
  assert.equal(await primary.isEnabled(), false); await close()
  report.checks.push('confirmation adds the selected entry once even with double click; existing entries cannot be duplicated')

  await open(packageProject); await ready()
  await page.setViewportSize({ width: 480, height: 820 })
  await page.screenshot({ path: path.join(evidence, 'compact-small-window.png') })
  assert.equal(await page.locator('.add-modal').evaluate(el => el.scrollWidth <= el.clientWidth), true)
  await close()
  for (const folder of allProjects) assert.equal(existsSync(path.join(folder, 'executed.txt')), false)
  for (const [file, bytes] of originalFiles) assert.equal(readFileSync(file, 'utf8'), bytes)
  assert.equal(calls.some(call => /\/(start|restart|resolve-ports)$/.test(call.url)), false)
  assert.deepEqual(pageErrors, [])
  report.checks.push('small-window layout fits; no project file changed, entry ran, dependency installed, or start endpoint called')
  report.requests = calls.map(call => call.url)
  report.passed = true
} catch (error) {
  report.error = String(error)
  if (page) await page.screenshot({ path: path.join(evidence, 'failure.png') }).catch(() => {})
  throw error
} finally {
  for (const release of gates) release()
  if (context) await context.tracing.stop({ path: path.join(evidence, 'browser-trace.zip') }).catch(() => {})
  if (browser) await browser.close()
  if (vite) await vite.close()
  if (backend && backend.exitCode === null) {
    try { await request('/api/desktop/shutdown', 'POST', {}); await until(() => backend.exitCode !== null, 10000) } catch { backend.kill() }
  }
  writeFileSync(path.join(evidence, 'result.json'), JSON.stringify(report, null, 2) + '\n')
  console.log(`${report.passed ? 'PASS' : 'FAIL'}: ${path.join(evidence, 'result.json')}`)
}
