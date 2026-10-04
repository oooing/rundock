// End-to-end acceptance: real Windows owners, isolated SQLite/API and real Vue UI.
// Build sidecar/.tmp/port-resolution-validation.exe, then run this script.
import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs'
import { createRequire } from 'node:module'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const dir = path.join(root, 'sidecar/.tmp', `port-resolution-${Date.now()}`)
const evidence = path.join(dir, 'evidence')
mkdirSync(evidence, { recursive: true })
const report = { passed: false, boundary: 'real Windows processes, real API, real Vue browser', checks: [] }
const children = []
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
const exited = child => child.exitCode !== null || child.signalCode !== null
async function until(check, timeout = 45000) {
  const end = Date.now() + timeout
  while (Date.now() < end) { const result = await check(); if (result) return result; await pause(250) }
  throw new Error('Timed out waiting for acceptance state')
}
async function freePort() {
  const server = net.createServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}
function launch(file, args, env = {}) {
  const child = spawn(file, args, { windowsHide: true, cwd: dir, env: { ...process.env, ...env }, stdio: ['ignore', 'ignore', 'pipe'] })
  children.push(child)
  child.stderr.on('data', chunk => { child.lastError = ((child.lastError || '') + chunk.toString()).slice(-4000) })
  return child
}
const apiPort = await freePort(), uiPort = await freePort()
const base = `http://127.0.0.1:${apiPort}`, uiBase = `http://127.0.0.1:${uiPort}`
async function raw(route, method = 'GET', body, headers = {}) {
  const response = await fetch(base + route, { method, headers: { 'Content-Type': 'application/json', ...headers },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(25000) })
  return { status: response.status, body: await response.json() }
}
async function request(route, method = 'GET', body, headers) {
  const result = await raw(route, method, body, headers)
  assert.ok(result.status < 300, `${method} ${route}: ${JSON.stringify(result)}`)
  return result.body
}
async function reachable(port) {
  try { return (await fetch(`http://127.0.0.1:${port}`, { signal: AbortSignal.timeout(1000) })).ok } catch { return false }
}
async function tcpReachable(port) {
  return new Promise(resolve => {
    const socket = net.createConnection({ host: '127.0.0.1', port })
    const finish = result => { socket.destroy(); resolve(result) }
    socket.setTimeout(1000, () => finish(false))
    socket.once('error', () => finish(false))
    socket.once('connect', () => finish(true))
  })
}
function exclusiveDualOwner(port) {
  // Compile a disposable .NET fixture with Windows SO_EXCLUSIVEADDRUSE.
  // Unlike Node's standard socket, its IPv4 overlap may fail with WSAEACCES.
  const source = path.join(dir, 'exclusive-owner.cs'), executable = path.join(dir, 'exclusive-owner.exe')
  writeFileSync(source, `using System;using System.Net;using System.Net.Sockets;
class Owner {static void Main(string[] args) {
var socket=new Socket(AddressFamily.InterNetworkV6,SocketType.Stream,ProtocolType.Tcp);
socket.DualMode=true;socket.ExclusiveAddressUse=true;
socket.Bind(new IPEndPoint(IPAddress.IPv6Any,int.Parse(args[0])));socket.Listen(8);
while(true) {var client=socket.Accept();client.Close();}
}}`)
  const compiler = path.join(process.env.WINDIR || 'C:/Windows', 'Microsoft.NET/Framework64/v4.0.30319/csc.exe')
  assert.ok(existsSync(compiler), 'The Windows .NET compiler is required for exclusive socket acceptance')
  execFileSync(compiler, ['/nologo', '/target:exe', `/out:${executable}`, source], { windowsHide: true })
  return launch(executable, [String(port)])
}
function fixtureFiles(name, port) {
  const cwd = path.join(dir, 'projects', name)
  mkdirSync(cwd, { recursive: true })
  const script = path.join(cwd, 'start.cmd'), server = path.join(cwd, 'server.cjs')
  writeFileSync(server, `const fs=require('node:fs');const http=require('node:http');\n` +
    `fs.appendFileSync(__dirname+'/starts.txt','start\\n');\n` +
    `http.createServer((q,s)=>s.end('fixture')).listen(${port},'127.0.0.1',()=>console.log('http://127.0.0.1:${port}'));\n`)
  writeFileSync(script, `@echo off\r\nrem rundock:ready http://127.0.0.1:${port}/\r\nrem rundock:open http://127.0.0.1:${port}/\r\n"${process.execPath}" "%~dp0server.cjs"\r\n`)
  return { cwd, script, server }
}
async function makeApp(name, port) {
  const files = fixtureFiles(name, port)
  const candidate = await request('/api/import', 'POST', { scriptPath: files.script })
  const app = await request('/api/apps', 'POST', { name, cwd: files.cwd, entryScript: candidate.entryScript,
    adapterType: candidate.adapterType, cmd: candidate.cmd, args: candidate.args, env: candidate.env,
    scriptHash: candidate.scriptHash, portHints: candidate.portHints, healthUrl: `http://127.0.0.1:${port}` })
  return { ...app, ...files }
}
function owner(port, name) {
  const files = fixtureFiles(`external-${name}`, port)
  return launch(process.execPath, [files.server])
}
const route = (app, action) => `/api/apps/${app.id}/${action}`
async function inspect(app) { return request(route(app, 'port-resolution'), 'POST', {}) }
async function stop(app) { await request(route(app, 'stop'), 'POST', {}) }
async function running(app) {
  return until(async () => { const view = await request(`/api/apps/${app.id}`); return view.status === 'running' && view.runId && view })
}
const starts = app => existsSync(path.join(app.cwd, 'starts.txt')) ? readFileSync(path.join(app.cwd, 'starts.txt'), 'utf8').trim().split('\n').length : 0
async function reservedPort() {
  const text = execFileSync('netsh.exe', ['int', 'ipv4', 'show', 'excludedportrange', 'protocol=tcp'], { windowsHide: true, encoding: 'utf8' })
  for (const match of text.matchAll(/^\s+(\d+)\s+(\d+)\s*\*?\s*$/gm)) {
    const first = Number(match[1]), last = Number(match[2])
    for (const port of [first, Math.min(first + 1, last)]) {
      if (port < 1024) continue
      const socket = net.createServer()
      const code = await new Promise(resolve => {
        socket.once('error', error => resolve(error.code))
        socket.listen(port, '127.0.0.1', () => socket.close(() => resolve('available')))
      })
      if (code === 'EACCES') return port
    }
  }
  return null
}
let backend, vite, browser, context, page
try {
  const harness = path.join(dir, 'ui'); mkdirSync(harness)
  const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
  writeFileSync(path.join(harness, 'index.html'), '<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
  writeFileSync(path.join(harness, 'main.ts'), `import {createApp,h,ref} from 'vue';import {createPinia} from 'pinia';\n` +
    `import AppCard from '${source}/components/AppCard.vue';import {useAppsStore} from '${source}/stores/apps.ts';import '${source}/styles.css';\n` +
    `window.__LAUNCHER_BASE__=${JSON.stringify(base)};const selected=ref('');window.selectFixtureApp=id=>{selected.value=id};\n` +
    `createApp({setup(){const store=useAppsStore();window.refreshFixtureApps=()=>store.load();return()=>{const app=store.apps.find(a=>a.id===selected.value);return app?h(AppCard,{app,groups:[]}):null}}}).use(createPinia()).mount('#app');\n`)
  vite = await createServer({ configFile: false, root: harness, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } },
    optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: uiPort, strictPort: true, fs: { allow: [root] } } })
  await vite.listen()
  backend = launch(path.join(root, 'sidecar/.tmp/port-resolution-validation.exe'), ['-port', String(apiPort)],
    { LAUNCHER_DATA_DIR: path.join(dir, 'data'), LAUNCHER_UI_ORIGIN: uiBase })
  await until(async () => { try { return (await request('/api/health')).status === 'ok' } catch { return false } })
  const require = createRequire(import.meta.url)
  const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
  browser = await chromium.launch({ channel: process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge', headless: true })
  context = await browser.newContext({ viewport: { width: 1100, height: 850 } })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage()
  const pageErrors = []; page.on('pageerror', error => pageErrors.push(error.message))
  await page.goto(uiBase)
  await page.waitForFunction(() => typeof window.selectFixtureApp === 'function' && typeof window.refreshFixtureApps === 'function')
  async function show(app) {
    await request(route(app, 'runtime-check'), 'POST', {})
    await page.evaluate(async id => { window.selectFixtureApp(id); await window.refreshFixtureApps() }, app.id)
    await page.getByRole('button', { name: app.name, exact: true }).waitFor()
  }

  const port = await freePort(), external = await makeApp('外部占用验收', port)
  let other = owner(port, 'first'); await until(() => reachable(port))
  let plan = await inspect(external)
  assert.equal(plan.state, 'conflict'); assert.equal(plan.canResolve, true); assert.equal(plan.conflicts[0].pid, other.pid)
  assert.ok(plan.conflicts[0].name); assert.ok(plan.confirmationToken)
  assert.equal((await raw(route(external, 'port-resolution'), 'POST', {}, { Origin: 'https://untrusted.example' })).status, 403)
  assert.equal((await raw(route(external, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken }, { Origin: 'https://untrusted.example' })).status, 403)
  assert.equal(other.exitCode, null)
  report.checks.push('real external application identified; foreign websites cannot inspect/close it')

  await show(external)
  await page.getByRole('button', { name: '关闭占用程序并启动', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.waitFor(); await dialog.getByText(String(other.pid), { exact: false }).waitFor()
  assert.equal(await page.evaluate(() => document.activeElement?.textContent?.trim()), '取消', 'cancellation must receive initial focus')
  await page.screenshot({ path: path.join(evidence, 'external-confirmation.png') })
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await until(async () => !await dialog.isVisible())
  assert.equal(other.exitCode, null); assert.equal(starts(external), 0)
  report.checks.push('real Vue confirmation names the process; cancel closes nothing')

  // Changing the real listener invalidates an already issued confirmation.
  plan = await inspect(external); other.kill(); await until(() => exited(other))
  other = owner(port, 'replacement'); await until(() => reachable(port))
  const stale = await raw(route(external, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken })
  assert.equal(stale.status, 409); assert.equal(other.exitCode, null); assert.equal(starts(external), 0)
  report.checks.push('changed owner rejects stale confirmation without ending the replacement')

  await show(external)
  await page.getByRole('button', { name: '关闭占用程序并启动', exact: true }).click()
  await dialog.waitFor()
  await dialog.getByRole('button', { name: '关闭并启动', exact: true }).click()
  await running(external); await until(() => exited(other)); assert.equal(starts(external), 1)
  await page.evaluate(() => window.refreshFixtureApps())
  await page.screenshot({ path: path.join(evidence, 'external-started.png') })
  const already = await inspect(external)
  assert.equal(already.state, 'running'); assert.equal(already.canResolve, false)
  await stop(external); await until(async () => !await reachable(port))
  report.checks.push('browser confirmation ends only the named external owner and starts once; running project cannot be killed by recovery')

  const managedPort = await freePort(), ownerApp = await makeApp('受管理占用项目', managedPort), target = await makeApp('接续启动项目', managedPort)
  await request(route(ownerApp, 'start'), 'POST', {}); await running(ownerApp)
  plan = await inspect(target)
  assert.equal(plan.canResolve, true); assert.equal(plan.conflicts[0].managedAppId, ownerApp.id)
  assert.equal(plan.conflicts[0].managedAppName, ownerApp.name)
  await request(route(target, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken })
  await running(target); assert.equal((await request(`/api/apps/${ownerApp.id}`)).status, 'stopped')
  assert.equal(starts(target), 1); await stop(target)
  report.checks.push('managed owner uses normal project stop before starting the target')

  const scriptPort = await freePort(), changedScript = await makeApp('脚本变化验收', scriptPort)
  const scriptOwner = owner(scriptPort, 'script'); await until(() => reachable(scriptPort))
  plan = await inspect(changedScript)
  writeFileSync(changedScript.script, readFileSync(changedScript.script, 'utf8') + '\r\nrem user changed the startup script\r\n')
  assert.equal((await raw(route(changedScript, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken })).status, 409)
  assert.equal(scriptOwner.exitCode, null); assert.equal(starts(changedScript), 0)
  scriptOwner.kill(); await until(() => exited(scriptOwner))
  report.checks.push('changed script bytes reject confirmation before ending an owner')

  const configPort = await freePort(), changed = await makeApp('配置变化验收', configPort)
  other = owner(configPort, 'config'); await until(() => reachable(configPort))
  plan = await inspect(changed)
  await request(`/api/apps/${changed.id}`, 'PATCH', { name: '配置变化后的项目' })
  assert.equal((await raw(route(changed, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken })).status, 409)
  assert.equal(other.exitCode, null)
  plan = await inspect(changed)
  const simultaneous = await Promise.all([raw(route(changed, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken }),
    raw(route(changed, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken })])
  assert.equal(simultaneous.filter(result => result.status === 200).length, 1)
  assert.equal(simultaneous.filter(result => result.status === 409).length, 1)
  await running(changed); assert.equal(starts(changed), 1); await stop(changed)
  report.checks.push('changed configuration rejects confirmation; concurrent reuse starts exactly once')

  const remotePort = await freePort(), remoteHealth = await makeApp('远程健康地址验收', remotePort)
  writeFileSync(remoteHealth.script, readFileSync(remoteHealth.script, 'utf8').replace(/^rem rundock:(?:ready|open).*\r?\n/gm, ''))
  await request(`/api/apps/${remoteHealth.id}`, 'PATCH', { healthUrl: `https://example.com:${remotePort}` })
  const remoteOwner = owner(remotePort, 'remote-health'); await until(() => reachable(remotePort))
  plan = await inspect(remoteHealth)
  assert.equal(plan.state, 'clear', 'a remote health endpoint is not a local listening-port declaration')
  assert.equal(plan.conflicts.length, 0); assert.equal(plan.canResolve, false)
  assert.equal(remoteOwner.exitCode, null)
  remoteOwner.kill(); await until(() => exited(remoteOwner))
  report.checks.push('remote health URLs and historical hints cannot grant authority to close a local program')

  const protectedApp = await makeApp('后台保护验收', apiPort)
  plan = await inspect(protectedApp)
  assert.equal(plan.canResolve, false); assert.ok(plan.conflicts.some(conflict => !conflict.canClose))
  assert.equal((await request('/api/health')).status, 'ok')
  report.checks.push('serving backend cannot be closed')

  // An IPv6-only listener cannot conflict with a declared IPv4-only endpoint.
  const familyPort = await freePort(), ipv4 = await makeApp('地址族隔离验收', familyPort)
  const ipv6Owner = launch(process.execPath, ['-e', `require('node:http').createServer((q,s)=>s.end('ipv6')).listen({port:${familyPort},host:'::1',ipv6Only:true})`])
  await until(async () => { try { return (await fetch(`http://[::1]:${familyPort}`, { signal: AbortSignal.timeout(1000) })).ok } catch { return false } })
  plan = await inspect(ipv4)
  assert.equal(plan.state, 'clear', 'a different address family must not cause a false conflict')
  assert.equal(plan.conflicts.length, 0)
  await request(route(ipv4, 'start'), 'POST', {}); await running(ipv4)
  assert.equal(ipv6Owner.exitCode, null, 'IPv6 owner must remain untouched')
  await stop(ipv4); ipv6Owner.kill(); await until(() => exited(ipv6Owner))
  report.checks.push('IPv6-only owner does not block an IPv4-only project or gain termination authority')

  const dualPort = await freePort(), dualTarget = await makeApp('双栈占用验收', dualPort)
  const dualOwner = launch(process.execPath, ['-e', `require('node:http').createServer((q,s)=>s.end('dual')).listen({port:${dualPort},host:'::',ipv6Only:false})`])
  await until(() => reachable(dualPort))
  plan = await inspect(dualTarget)
  assert.equal(plan.state, 'conflict', 'dual-stack IPv6 wildcard really occupies IPv4 too')
  assert.equal(plan.canResolve, true); assert.ok(plan.conflicts.some(conflict => conflict.pid === dualOwner.pid))
  await show(dualTarget)
  await page.getByRole('button', { name: '关闭占用程序并启动', exact: true }).waitFor()
  await request(route(dualTarget, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken })
  await running(dualTarget); await until(() => exited(dualOwner)); await stop(dualTarget)
  const wildTarget = await makeApp('IPv6通配隔离验收', dualPort)
  const wildOwner = launch(process.execPath, ['-e', `require('node:http').createServer((q,s)=>s.end('ipv6-only')).listen({port:${dualPort},host:'::',ipv6Only:true})`])
  await until(async () => { try { return (await fetch(`http://[::1]:${dualPort}`, { signal: AbortSignal.timeout(1000) })).ok } catch { return false } })
  plan = await inspect(wildTarget)
  assert.equal(plan.state, 'clear', 'IPv6-only wildcard does not occupy IPv4')
  assert.equal(plan.conflicts.length, 0)
  await request(route(wildTarget, 'start'), 'POST', {}); await running(wildTarget)
  assert.equal(wildOwner.exitCode, null); await stop(wildTarget)
  wildOwner.kill(); await until(() => exited(wildOwner))
  report.checks.push('dual-stack wildcard identifies the real owner; IPv6-only wildcard remains untouched')

  const exclusivePort = await freePort(), exclusiveTarget = await makeApp('独占双栈验收', exclusivePort)
  const exclusiveOwner = exclusiveDualOwner(exclusivePort)
  await until(() => tcpReachable(exclusivePort))
  plan = await inspect(exclusiveTarget)
  assert.equal(plan.state, 'conflict', 'an exclusive application socket is not a Windows reservation')
  assert.equal(plan.canResolve, true); assert.equal(plan.reservedPorts.length, 0)
  assert.ok(plan.conflicts.some(conflict => conflict.pid === exclusiveOwner.pid))
  assert.equal((await request(`/api/apps/${exclusiveTarget.id}`)).runtimeCheck.state, 'conflict', 'card refresh must retain fresh classification')
  await request(route(exclusiveTarget, 'resolve-ports'), 'POST', { confirmationToken: plan.confirmationToken })
  await running(exclusiveTarget); await until(() => exited(exclusiveOwner)); await stop(exclusiveTarget)
  report.checks.push('Windows exclusive dual-stack application is identified rather than misclassified as a reservation')

  const reserved = await reservedPort()
  if (reserved) {
    const system = await makeApp('系统保留验收', reserved)
    plan = await inspect(system)
    assert.equal(plan.state, 'reserved'); assert.equal(plan.canResolve, false)
    assert.ok(plan.reservedPorts.includes(reserved)); assert.equal(plan.conflicts.length, 0)
    assert.ok((await raw(route(system, 'start'), 'POST', {})).status >= 400)
    assert.equal(starts(system), 0)
    await show(system)
    assert.equal(await page.getByRole('button', { name: '关闭占用程序并启动', exact: true }).count(), 0)
    await page.getByText('系统保留', { exact: false }).first().waitFor()
    await page.screenshot({ path: path.join(evidence, 'system-reserved.png') })
    report.reservedPort = reserved
    report.checks.push('real Windows reservation is distinguished from application ownership; no close action or script execution')
  } else report.skipped = ['No current Windows exclusion produced EACCES; reservation acceptance requires such a port']
  assert.deepEqual(pageErrors, [])
  report.passed = true
} catch (error) {
  report.error = String(error)
  if (page) await page.screenshot({ path: path.join(evidence, 'failure.png') }).catch(() => {})
  throw error
} finally {
  if (context) await context.tracing.stop({ path: path.join(evidence, 'browser-trace.zip') }).catch(() => {})
  if (browser) await browser.close()
  if (vite) await vite.close()
  if (backend && !exited(backend)) {
    try { await request('/api/desktop/shutdown', 'POST', {}); await until(() => exited(backend), 10000) } catch { backend.kill() }
  }
  for (const child of children) if (!exited(child)) child.kill()
  writeFileSync(path.join(evidence, 'result.json'), JSON.stringify(report, null, 2) + '\n')
  console.log(`${report.passed ? 'PASS' : 'FAIL'}: ${path.join(evidence, 'result.json')}`)
}
