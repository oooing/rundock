// End-to-end startup guard check against disposable processes and data only.
// Build sidecar/.tmp/runtime-stale-ports-validation.exe before running this file.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const dir = path.join(root, 'sidecar', '.tmp', `runtime-stale-ports-${Date.now()}`)
const project = path.join(dir, 'project')
const data = path.join(dir, 'data')
mkdirSync(project, { recursive: true })
mkdirSync(data, { recursive: true })

const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const exited = child => child.exitCode !== null || child.signalCode !== null
async function until(check, timeout = 35000) {
  const end = Date.now() + timeout
  while (Date.now() < end) {
    const value = await check()
    if (value) return value
    await pause(250)
  }
  throw new Error('Timed out waiting for runtime state')
}
async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}
const ports = new Set()
while (ports.size < 3) ports.add(await freePort())
const [oldPort, newPort, apiPort] = [...ports]
const base = `http://127.0.0.1:${apiPort}`
const endpoint = port => `http://127.0.0.1:${port}`
async function request(route, method = 'GET', body) {
  const response = await fetch(base + route, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15000),
  })
  const result = await response.json()
  if (!response.ok) throw new Error(`${method} ${route}: ${response.status} ${JSON.stringify(result)}`)
  return result
}
async function reachable(port) {
  try { return (await fetch(endpoint(port), { signal: AbortSignal.timeout(1000) })).ok }
  catch { return false }
}
function launch(file, args, extra = {}) {
  const child = spawn(file, args, {
    windowsHide: true, cwd: dir,
    env: { ...process.env, ...extra }, stdio: ['ignore', 'ignore', 'pipe'],
  })
  child.stderr.on('data', chunk => { child.lastError = (child.lastError || '') + chunk.toString() })
  return child
}

let backend, stranger, app
const script = path.join(project, 'start.cmd')
const server = path.join(project, 'server.cjs')
const starts = path.join(project, 'starts.txt')
writeFileSync(server, `const fs = require('node:fs'); const http = require('node:http');\n` +
  `fs.appendFileSync(${JSON.stringify(starts)}, process.env.SERVICE_PORT + '\\n');\n` +
  `http.createServer((req, res) => res.end('fixture')).listen(Number(process.env.SERVICE_PORT), '127.0.0.1', () => console.log('http://127.0.0.1:' + process.env.SERVICE_PORT));\n`)
writeFileSync(script, `@echo off\r\n"${process.execPath}" "%~dp0server.cjs"\r\n`)

try {
  backend = launch(path.join(root, 'sidecar/.tmp/runtime-stale-ports-validation.exe'), ['-port', String(apiPort)], { LAUNCHER_DATA_DIR: data })
  await until(async () => { try { return (await request('/api/health')).status === 'ok' } catch { return false } })
  const candidate = await request('/api/import', 'POST', { scriptPath: script })
  app = await request('/api/apps', 'POST', {
    name: 'Stale port fixture', entryScript: script, cwd: project,
    adapterType: candidate.adapterType, cmd: candidate.cmd, args: candidate.args,
    env: { SERVICE_PORT: String(oldPort) }, scriptHash: candidate.scriptHash,
    portHints: [], healthUrl: endpoint(oldPort),
  })
  await request(`/api/apps/${app.id}/start`, 'POST', {})
  await until(async () => (await reachable(oldPort)) && (await request(`/api/apps/${app.id}`)).status === 'running')
  await until(async () => (await request(`/api/apps/${app.id}`)).knownServices.some(service => service.port === oldPort))
  await request(`/api/apps/${app.id}/stop`, 'POST', {})
  await until(async () => !(await reachable(oldPort)))

  // The old service row and lastUrl deliberately remain, but are no longer
  // evidence that this project's next run must bind the old port.
  await request(`/api/apps/${app.id}`, 'PATCH', { env: { SERVICE_PORT: String(newPort) }, healthUrl: endpoint(newPort) })
  stranger = launch(process.execPath, ['-e', `require('node:http').createServer((q,s)=>s.end('other')).listen(${oldPort},'127.0.0.1')`])
  await until(() => reachable(oldPort))
  let state = await request(`/api/apps/${app.id}/runtime-check`, 'POST', {})
  assert.equal(state.runtimeCheck.state, 'clear', 'historical port must not block the next run')
  await request(`/api/apps/${app.id}/start`, 'POST', {})
  await until(() => reachable(newPort))
  state = await until(async () => {
    const current = await request(`/api/apps/${app.id}`)
    return current.status === 'running' && current
  })
  assert.equal(state.status, 'running')
  assert.ok(await reachable(oldPort), 'unrelated port owner must not be stopped')
  await request(`/api/apps/${app.id}/stop`, 'POST', {})
  await until(async () => !(await reachable(newPort)))

  stranger.kill()
  await until(() => exited(stranger))
  stranger = launch(process.execPath, ['-e', `require('node:http').createServer((q,s)=>s.end('other')).listen(${newPort},'127.0.0.1')`])
  await until(() => reachable(newPort))
  state = await request(`/api/apps/${app.id}/runtime-check`, 'POST', {})
  assert.equal(state.runtimeCheck.state, 'conflict', 'current explicit port must still block')
  await assert.rejects(request(`/api/apps/${app.id}/start`, 'POST', {}))
  assert.ok(await reachable(newPort), 'rejected start must not stop unrelated owner')

  const evidence = {
    result: 'PASS', project: 'disposable fixture', oldPort, newPort,
    scenarios: ['historical port ignored', 'current port conflict blocked', 'unrelated process untouched'],
    starts: existsSync(starts) ? readFileSync(starts, 'utf8').trim().split('\n') : [],
  }
  const output = path.join(dir, 'result.json')
  writeFileSync(output, JSON.stringify(evidence, null, 2) + '\n')
  console.log(`PASS: ${output}`)
} finally {
  if (app && backend && !exited(backend)) {
    try { await request(`/api/apps/${app.id}/stop`, 'POST', {}) } catch { /* already stopped */ }
  }
  if (stranger && !exited(stranger)) stranger.kill()
  if (backend && !exited(backend)) {
    try { await request('/api/desktop/shutdown', 'POST', {}) } catch { backend.kill() }
  }
}
