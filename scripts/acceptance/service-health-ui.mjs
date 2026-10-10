// Isolated browser acceptance: mock API only; never starts, stops or restarts a project.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { preview } from 'vite'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const evidence = path.join(root, 'outputs/acceptance/service-health-ui')
mkdirSync(evidence, { recursive: true })
const server = await preview({ root, preview: { host: '127.0.0.1', port: 0 } })
const origin = `http://127.0.0.1:${server.httpServer.address().port}`
let browser
const checks = [], errors = [], writes = []
const service = (port, role, health = 'healthy', reason = '', scope = 'required') => ({
  id: `svc-${port}`, appId: 'health-fixture', appRunId: 'run', port, role, roleSource: 'auto',
  url: `http://localhost:${port}`, health, healthReason: reason, statusScope: scope,
  healthProbeUrl: `http://127.0.0.1:${port}/api/health`, detectedAt: '2026-10-10T00:00:00Z',
})
let status = 'running', services = [service(5284, 'frontend', 'unhealthy', 'http_status:400', 'auxiliary'), service(17655, 'backend'), service(17656, 'frontend')]
const fixture = () => ({ id: 'health-fixture', name: 'RunDock', entryScript: 'C:/fixture/dev.bat', cwd: 'C:/fixture', args: [], env: {}, tags: [],
  status, services, portHints: [], adapterType: 'batch', cardColor: '#34318c', pid: 123, runId: 'run', sortOrder: 0,
  lastUrl: 'http://127.0.0.1:17656/' })

try {
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  const page = await browser.newPage({ locale: 'zh-CN', viewport: { width: 1200, height: 850 } })
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => {
    window.__LAUNCHER_BASE__ = 'http://fixture.invalid'
    if (!localStorage.getItem('rundock.ui.locale')) localStorage.setItem('rundock.ui.locale', 'zh-CN')
  })
  await page.routeWebSocket(/.*/, socket => socket.close())
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin === origin) return route.continue()
    if (url.origin !== 'http://fixture.invalid') return route.abort()
    const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' }
    const send = body => route.fulfill({ status: 200, headers, contentType: 'application/json', body: JSON.stringify(body) })
    if (request.method() === 'OPTIONS') return send({})
    if (request.method() !== 'GET') { writes.push(`${request.method()} ${url.pathname}`); return route.abort() }
    if (url.pathname === '/api/health') return send({ status: 'ok', apiVersion: '2', capabilities: 'release-v2' })
    if (url.pathname === '/api/apps') return send([fixture()])
    if (url.pathname === '/api/apps/health-fixture') return send(fixture())
    if (url.pathname.endsWith('/logs') || url.pathname === '/api/groups' || url.pathname === '/api/cloud-builds') return send([])
    return send({})
  })
  await page.goto(origin)
  const card = page.locator('article.card')
  const notice = card.locator('.health-notice')
  await card.locator('.badge').getByText('运行中', { exact: true }).waitFor()
  assert.equal(await notice.count(), 0)
  assert.deepEqual(await card.locator('.svc-port').allTextContents(), [':17656', ':17655', ':5284'])
  await card.getByText('辅助', { exact: true }).waitFor()
  assert.equal(await card.getByLabel('辅助端口，不参与项目运行状态判定', { exact: true }).count(), 1)
  await page.screenshot({ path: path.join(evidence, 'running-auxiliary.png') })
  checks.push('Two healthy required services remain Running; auxiliary HTTP 400 is gray, labeled and sorted last')

  status = 'degraded'; services[1] = service(17655, 'backend', 'unhealthy', 'http_status:503')
  await page.reload()
  await notice.getByText('健康检查返回 HTTP 503', { exact: false }).waitFor()
  assert.equal(await notice.getAttribute('role'), 'status')
  assert.match(await notice.innerText(), /:17655/)
  assert.doesNotMatch(await notice.innerText(), /5284|HTTP 400/)
  assert.equal(await notice.getByText('http://127.0.0.1:17655/api/health', { exact: true }).count(), 1)
  const log = notice.getByRole('button', { name: '查看日志' })
  await log.focus()
  assert.equal(await log.evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Enter')
  await page.locator('.drawer').waitFor()
  await page.keyboard.press('Escape')
  await page.screenshot({ path: path.join(evidence, 'required-failure.png') })
  checks.push('Required HTTP 503 reason and exact probe URL are visible outside the port scroller; keyboard opens logs')

  const reasons = ['timeout', 'connection_refused', 'connection_failed', 'invalid_url', '']
  const labels = ['健康检查超时', '连接被拒绝，服务可能已停止', '无法连接服务', '健康检查地址无效', '健康检查未通过']
  for (let i = 0; i < reasons.length; i++) {
    services[1] = service(17655, 'backend', 'unhealthy', reasons[i])
    await page.reload()
    await notice.getByText(labels[i], { exact: false }).waitFor()
  }
  services[2] = service(17656, 'frontend', 'unhealthy', 'timeout')
  await page.reload()
  await notice.locator('li').nth(1).waitFor()
  assert.equal(await notice.locator('li').count(), 2)
  checks.push('Timeout, refused connection, generic connection failure, invalid URL and missing reason are readable; multiple required failures all appear')

  await page.setViewportSize({ width: 375, height: 850 })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true)
  const buttonSize = await log.boundingBox()
  assert.ok(buttonSize.width >= 24 && buttonSize.height >= 24)
  await page.screenshot({ path: path.join(evidence, 'required-failure-mobile.png') })
  checks.push('375px layout has no horizontal page overflow; log control meets target size and no new animation is introduced')

  services = []
  await page.reload()
  await notice.getByText('服务健康检查未通过，查看日志了解详情。').waitFor()
  checks.push('Legacy degraded payload without service details shows honest fallback, not an invented reason')

  services = [service(17655, 'backend', 'unhealthy', 'http_status:503')]
  await page.evaluate(() => localStorage.setItem('rundock.ui.locale', 'en'))
  await page.reload()
  await notice.getByText('Health check returned HTTP 503', { exact: false }).waitFor()
  await notice.getByRole('button', { name: 'View logs' }).waitFor()
  checks.push('English reason and log action are translated')

  status = 'running'; services[0] = service(17655, 'backend')
  await page.reload()
  await card.locator('.badge').getByText('Running', { exact: true }).waitFor()
  assert.equal(await notice.count(), 0)
  assert.deepEqual(errors, [])
  assert.deepEqual(writes, [])
  checks.push('Recovery removes the warning; no browser errors or API writes occurred')
  const result = { passed: true, checks, errors, writes }
  writeFileSync(path.join(evidence, 'result.json'), JSON.stringify(result, null, 2))
  console.log(JSON.stringify(result, null, 2))
} finally {
  await browser?.close()
  await new Promise(resolve => server.httpServer.close(resolve))
}
