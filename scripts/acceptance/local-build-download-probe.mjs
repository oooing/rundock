// Isolated browser end-to-end diagnostic, not a unit test.
// Failure inventory: guard rejection, preflight rejection, malformed file response,
// browser network failure, missing artifact bytes, leaked owned browser/server.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'
import { mkdirSync, cpSync, readFileSync, writeFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import http from 'node:http'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const stamp = `local-build-download-probe-${Date.now()}`
const work = path.join(root, 'sidecar/.tmp', stamp)
const evidence = path.join(root, 'outputs/acceptance', stamp)
mkdirSync(work, { recursive: true }); mkdirSync(evidence, { recursive: true })
const source = process.env.RUNDOCK_PROBE_DATA || path.join(root, 'sidecar/.tmp/local-build-1791016088939/data')
const relativeSource = path.relative(path.join(root, 'sidecar/.tmp'), source)
assert.match(relativeSource, /^local-build-(?:real-)?\d+[\\/]data$/, 'Probe only copies stopped, owned acceptance data')
cpSync(source, path.join(work, 'data'), { recursive: true })
const runID = process.env.RUNDOCK_PROBE_RUN_ID || '20261003T082917-bb985d814f76'
const artifactID = process.env.RUNDOCK_PROBE_ARTIFACT_ID || '2aed1bde1242eec63a809f265e65d24df07305cbd7134ee58b8956a18dfa1b2a'
const artifactRoute = `/api/local-builds/${runID}/artifacts/${artifactID}`
const report = { passed: false, source, evidence, requests: [], responses: [], extras: [], console: [], failures: [] }
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  await new Promise(resolve => server.close(resolve)); return port
}
const apiPort = await freePort(), uiPort = await freePort()
const apiBase = `http://127.0.0.1:${apiPort}`, uiBase = `http://127.0.0.1:${uiPort}`
report.apiBase = apiBase; report.uiBase = uiBase
let backend, browser, ui, backendLog = ''
try {
  ui = http.createServer((req, res) => {
    res.setHeader('Content-Type', 'text/html')
    res.end('<!doctype html><title>RunDock download probe</title><h1>RunDock download probe</h1><button id="probe">Check saved artifact</button>')
  })
  await new Promise(resolve => ui.listen(uiPort, '127.0.0.1', resolve))
  const executable = process.env.RUNDOCK_SIDECAR || path.join(root, 'sidecar/.tmp/local-build-final.exe')
  report.sidecar = { path: executable, sha256: createHash('sha256').update(readFileSync(executable)).digest('hex') }
  backend = spawn(executable, ['-port', String(apiPort)], {
    cwd: work, windowsHide: true, env: { ...process.env, LAUNCHER_DATA_DIR: path.join(work, 'data'), LAUNCHER_UI_ORIGIN: uiBase }, stdio: ['ignore', 'pipe', 'pipe']
  })
  backend.stdout.on('data', chunk => backendLog += chunk)
  backend.stderr.on('data', chunk => backendLog += chunk)
  let healthy = false
  for (let n = 0; n < 100; n++) {
    try { healthy = (await fetch(apiBase + '/api/health')).ok } catch {}
    if (healthy) break
    await delay(100)
  }
  assert.ok(healthy, 'Owned backend did not become healthy')
  const raw = await fetch(apiBase + artifactRoute, { headers: { Origin: uiBase, 'Content-Type': 'application/json' } })
  const rawBody = Buffer.from(await raw.arrayBuffer())
  report.native = { status: raw.status, headers: Object.fromEntries(raw.headers), length: rawBody.length, sha256: createHash('sha256').update(rawBody).digest('hex') }
  const require = createRequire(import.meta.url)
  const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
  browser = await chromium.launch({ headless: true,
    ...(process.env.RUNDOCK_BROWSER_EXECUTABLE ? { executablePath: process.env.RUNDOCK_BROWSER_EXECUTABLE } : {}),
    ...(process.env.RUNDOCK_BROWSER_CHANNEL ? { channel: process.env.RUNDOCK_BROWSER_CHANNEL } : {}) })
  const context = await browser.newContext()
  const page = await context.newPage(), cdp = await context.newCDPSession(page)
  await cdp.send('Network.enable')
  cdp.on('Network.requestWillBeSent', value => report.requests.push(value))
  cdp.on('Network.responseReceived', value => report.responses.push(value))
  cdp.on('Network.requestWillBeSentExtraInfo', value => report.extras.push({ kind: 'request', ...value }))
  cdp.on('Network.responseReceivedExtraInfo', value => report.extras.push({ kind: 'response', ...value }))
  cdp.on('Network.loadingFailed', value => report.failures.push(value))
  page.on('console', value => report.console.push({ type: value.type(), text: value.text() }))
  await page.goto(uiBase)
  report.initialPage = await page.locator('body').innerText()
  assert.ok(await page.locator('#probe').isVisible())
  report.browser = await page.evaluate(async ({ apiBase, runID, artifactRoute }) => {
    const results = []
    for (const route of [`/api/local-builds/${runID}`, artifactRoute]) {
      try {
        const response = await fetch(apiBase + route, { headers: { 'Content-Type': 'application/json' } })
        const bytes = await response.arrayBuffer()
        const digest = await crypto.subtle.digest('SHA-256', bytes)
        const sha256 = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
        results.push({ route, status: response.status, headers: Object.fromEntries(response.headers), length: bytes.byteLength, sha256 })
      } catch (error) { results.push({ route, error: error.message }) }
    }
    return results
  }, { apiBase, runID, artifactRoute })
  await page.screenshot({ path: path.join(evidence, 'browser.png') })
  report.passed = report.browser.every(value => value.status === 200)
    && report.browser.at(-1).length === report.native.length
    && report.browser.at(-1).sha256 === report.native.sha256
} catch (error) { report.error = error.stack }
finally {
  if (browser) await browser.close()
  if (backend && backend.exitCode === null) {
    const exited = new Promise(resolve => backend.once('exit', resolve))
    await fetch(apiBase + '/api/desktop/shutdown', { method: 'POST', signal: AbortSignal.timeout(5000) }).catch(() => {})
    await Promise.race([exited, delay(7000)])
    if (backend.exitCode === null) backend.kill()
  }
  if (ui) await new Promise(resolve => ui.close(resolve))
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  writeFileSync(path.join(evidence, 'backend.log'), backendLog)
}
console.log(JSON.stringify({ passed: report.passed, evidence, native: report.native, browser: report.browser, failures: report.failures, error: report.error }, null, 2))
process.exitCode = report.passed ? 0 : 1
