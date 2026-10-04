// Run a configured real local project through a separate production-shaped HTTP/SQLite service.
// All outputs and registration live under acceptance data. External source is read-only.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import path from 'node:path'
import net from 'node:net'
import { fileURLToPath } from 'node:url'
import { verifyRealLocalProject } from './local-build-real-project.mjs'

assert.ok(process.env.RUNDOCK_REAL_LOCAL_PROJECT, 'Set RUNDOCK_REAL_LOCAL_PROJECT explicitly')
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const dataDir = path.join(root, 'sidecar/.tmp', `local-build-real-${Date.now()}`)
const evidence = path.join(root, 'outputs/acceptance', path.basename(dataDir))
mkdirSync(dataDir, { recursive: true }); mkdirSync(evidence, { recursive: true })
const server = net.createServer()
await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
const port = server.address().port
await new Promise(resolve => server.close(resolve))
const base = `http://127.0.0.1:${port}`, origin = 'http://127.0.0.1:17656'
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const report = { passed: false, boundary: 'actual project commands in source snapshot + actual sidecar HTTP/SQLite + verified downloaded packages; no Git mutation, upload, public release or device installation', checks: [] }
let log = ''
const executable = process.env.RUNDOCK_SIDECAR || path.join(root, 'sidecar/.tmp/local-build-validation.exe')
report.sidecar = { path: executable, sha256: createHash('sha256').update(readFileSync(executable)).digest('hex') }
const backend = spawn(executable, ['-port', String(port)], {
  cwd: dataDir, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'], env: { ...process.env,
    PATH: path.dirname(process.execPath) + path.delimiter + process.env.PATH,
    LAUNCHER_DATA_DIR: path.join(dataDir, 'data'), LAUNCHER_UI_ORIGIN: origin },
})
backend.stdout.on('data', data => { log += data }); backend.stderr.on('data', data => { log += data })
async function response(route, method = 'GET', body) {
  return fetch(base + route, { method, headers: { Origin: origin, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(60000) })
}
async function request(route, method = 'GET', body) {
  const res = await response(route, method, body), value = await res.json()
  assert.ok(res.ok, `${method} ${route}: ${JSON.stringify(value)}`)
  return value
}
try {
  let ready = false
  const readyDeadline = Date.now() + 120000
  while (Date.now() < readyDeadline && backend.exitCode === null) {
    try { if ((await request('/api/health')).status === 'ok') { ready = true; break } } catch { }
    await pause(100)
  }
  assert.ok(ready, `Isolated acceptance backend must be healthy; exit=${backend.exitCode}; ${log.slice(-1500)}`)
  await verifyRealLocalProject({ request, response, evidence, report, pause })
  report.passed = true
} catch (error) { report.error = error.stack || String(error); process.exitCode = 1 }
finally {
  await fetch(base + '/api/desktop/shutdown', { method: 'POST', signal: AbortSignal.timeout(5000) }).catch(() => {})
  for (let attempt = 0; backend.exitCode === null && attempt < 70; attempt++) await pause(100)
  if (backend.exitCode === null) backend.kill()
  writeFileSync(path.join(evidence, 'backend.log'), log)
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify({ ...report, evidence }, null, 2))
}
