// Windows-only, real Go processes + production Rust transaction + real NSIS hooks.
// node scripts/acceptance/silent-update-cycle.mjs --prepare-only
//   Builds current sidecars, reuses the compiled production probe, and exercises stop/restore/failure, without
//   reading, building or running the installer helper. This is NOT a full pass.
// node scripts/acceptance/silent-update-cycle.mjs
//   Full cycle. Run only after the helper owner confirms its edits are ready.
// No installed RunDock, real data directory, fixed product port, or registry
// registration is used. Only this script and its fixture are acceptance code.
import assert from 'node:assert/strict'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import fs from 'node:fs'
import path from 'node:path'
import net from 'node:net'
import { fileURLToPath } from 'node:url'
import { createHash, randomUUID } from 'node:crypto'

assert.equal(process.platform, 'win32', 'This acceptance requires Windows')
const args = process.argv.slice(2)
assert.ok(args.every(arg => arg === '--prepare-only'), 'Only --prepare-only is supported')
const prepareOnly = args.includes('--prepare-only')
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const temporaryRoot = path.join(root, '.tmp')
const stamp = new Date().toISOString().replace(/[:.]/g, '-')
const directory = path.join(temporaryRoot, `silent-update-cycle-${stamp}-${process.pid}`)
const installed = path.join(directory, 'installed 中文')
const data = path.join(directory, 'data')
const output = path.join(root, 'outputs/acceptance', `silent-update-cycle-${stamp}.json`)
for (const folder of [installed, data, path.dirname(output)]) fs.mkdirSync(folder, { recursive: true })
const oldFile = path.join(temporaryRoot, 'update-backend-old.exe')
const newFile = path.join(temporaryRoot, 'update-backend-new.exe')
const existingProbe = path.join(root, 'src-tauri/target/debug/examples/update-transaction-probe.exe')
const probeFile = path.join(directory, 'update-transaction-probe.exe')
const installedSidecar = path.join(installed, 'launcher-sidecar.exe')
const installedMain = path.join(installed, 'launcher-platform.exe')
const reportFile = path.join(data, 'desktop-update.json')
const go = process.env.RUNDOCK_ACCEPTANCE_GO || path.join(process.env.USERPROFILE, 'go/bin/go.exe')
const makensis = process.env.RUNDOCK_ACCEPTANCE_NSIS || path.join(process.env.LOCALAPPDATA, 'tauri/NSIS/makensis.exe')
const exec = promisify(execFile)
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const sha256 = file => createHash('sha256').update(fs.readFileSync(file)).digest('hex')
const report = {
  passed: false, status: 'running', mode: prepareOnly ? 'prepare-only' : 'full',
  startedAt: new Date().toISOString(), directory, output,
  scope: 'Isolated real Windows Go backend, two Node HTTP projects, production Rust transaction probe; full mode also uses production NSIS prepare/file/commit hooks.',
  limitations: ['launcher-platform.exe is a valid Go PE payload and is never launched; desktop UI/IPC and real installation registration are outside this acceptance.'],
  githubPublished: false, forbiddenPorts: [17654, 17655, 17656],
  checks: [], commands: [], backendProcesses: [], observations: {}, cleanup: {},
}
const children = [], apps = [], reservedPorts = new Set(report.forbiddenPorts)
let backend, backendPort, base, installer, failure
const pending = () => JSON.parse(fs.readFileSync(reportFile, 'utf8'))
const savePending = value => fs.writeFileSync(reportFile, JSON.stringify(value, null, 2))
function check(name, evidence) {
  report.checks.push({ name, at: new Date().toISOString(), evidence })
  console.log(`PASS: ${name}`)
}
function sourceFiles(folder) {
  return fs.readdirSync(folder, { withFileTypes: true }).flatMap(entry => {
    const file = path.join(folder, entry.name)
    if (entry.isDirectory()) return sourceFiles(file)
    return entry.name.endsWith('.go') && !entry.name.endsWith('_test.go') ? [file] : []
  })
}
function sourceHashes(files) {
  return Object.fromEntries(files.sort().map(file => [path.relative(root, file), sha256(file)]))
}
async function run(file, argv = [], { timeout = 180_000, expectedCode = 0, cwd = directory } = {}) {
  const started = Date.now()
  const result = await exec(file, argv, { cwd, windowsHide: true, timeout, maxBuffer: 4e6 })
    .then(value => ({ ...value, code: 0 }), error => ({
      code: error.code, killed: error.killed, stdout: error.stdout || '', stderr: error.stderr || '', error: error.message,
    }))
  const evidence = { file, args: argv, durationMs: Date.now() - started, ...result }
  report.commands.push(evidence)
  assert.equal(result.code, expectedCode, `${path.basename(file)}: ${result.error || result.stderr}`)
  return evidence
}
async function until(label, fn, timeout = 30_000) {
  const end = Date.now() + timeout
  let lastError
  while (Date.now() < end) {
    try { const value = await fn(); if (value) return value } catch (error) { lastError = error }
    await sleep(100)
  }
  throw new Error(`Timed out: ${label}${lastError ? ` (${lastError.message}; ${lastError.cause?.code || lastError.cause?.message || ''})` : ''}`)
}
async function freePort() {
  for (;;) {
    const server = net.createServer()
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
    const port = server.address().port
    await new Promise(resolve => server.close(resolve))
    if (reservedPorts.has(port)) continue
    reservedPorts.add(port)
    return port
  }
}
async function raw(route, method = 'GET', body, timeout = 5000) {
  assert.ok(backendPort && !report.forbiddenPorts.includes(backendPort))
  const response = await fetch(base + route, { method,
    headers: { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(timeout) })
  return { status: response.status, body: await response.json() }
}
async function api(...arguments_) {
  const result = await raw(...arguments_)
  assert.ok(result.status >= 200 && result.status < 300, JSON.stringify(result))
  return result.body
}
const view = app => api(`/api/apps/${app.id}`)
const action = (app, operation) => api(`/api/apps/${app.id}/${operation}`, 'POST', {}, 15_000)
const running = app => until(`${app.name} running`, async () => {
  const value = await view(app)
  return value.status === 'running' && value.pid && value.runId && value
})
const cli = (operation, options) => run(probeFile, [operation, String(backendPort), data], options)
const events = app => fs.existsSync(app.events) ? fs.readFileSync(app.events, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) : []
const startCount = app => events(app).filter(event => event.event === 'start').length
async function projectHealth(app) {
  const response = await fetch(app.health, { signal: AbortSignal.timeout(2000) })
  assert.equal(response.status, 200)
  const identity = await response.json()
  assert.equal(identity.token, app.token, 'Port must belong to our fixture')
  return { status: response.status, ...identity }
}
async function snapshot() {
  return { backend: { ...await api('/api/health'), pid: backend.child.pid }, projects: await Promise.all(apps.map(async app => ({
    id: app.id, name: app.name, ...await view(app), starts: startCount(app),
    health: startCount(app) ? await projectHealth(app) : null,
  }))) }
}
function identities(value) {
  return { backend: value.backend, projects: value.projects.map(app => ({
    id: app.id, pid: app.pid, runId: app.runId, status: app.status, starts: app.starts, health: app.health,
  })) }
}
function launch(file, argv, logName, env = process.env) {
  const child = spawn(file, argv, { cwd: directory, env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
  const state = { child, file, done: false, code: null }
  children.push(state)
  child.once('error', error => { state.error = error.message; state.done = true })
  child.once('exit', (code, signal) => { Object.assign(state, { done: true, code, signal }) })
  for (const stream of [child.stdout, child.stderr]) stream.on('data', chunk => fs.appendFileSync(path.join(directory, logName), chunk))
  return state
}
async function startBackend(version) {
  // Do not attach to some unrelated process that won a port allocation race.
  const server = net.createServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(backendPort, '127.0.0.1', resolve) })
  await new Promise(resolve => server.close(resolve))
  backend = launch(installedSidecar, ['-port', String(backendPort)], `backend-${report.backendProcesses.length}.log`, {
    ...process.env, LAUNCHER_DATA_DIR: data,
  })
  report.backendProcesses.push({ pid: backend.child.pid, version, file: installedSidecar, sha256: sha256(installedSidecar) })
  const health = await until(`backend ${version}`, async () => {
    if (backend.done) throw new Error(`Backend exited: ${backend.code} ${backend.error || ''}`)
    if (!fs.existsSync(path.join(data, 'sidecar.port'))) return false
    assert.equal(Number(fs.readFileSync(path.join(data, 'sidecar.port'), 'utf8')), backendPort)
    return api('/api/health')
  })
  assert.equal(health.version, version)
  assert.ok(health.instanceId)
  return health
}
async function makeProject(name) {
  const folder = path.join(directory, name)
  fs.mkdirSync(folder)
  const port = await freePort(), token = randomUUID()
  const entry = path.join(folder, 'start.cmd'), server = path.join(folder, 'server.cjs')
  fs.writeFileSync(entry, '@echo off\r\n')
  fs.copyFileSync(path.join(root, 'scripts/acceptance/fixtures/silent-update-http.cjs'), server)
  const app = await api('/api/apps', 'POST', {
    name, entryScript: entry, cwd: folder, adapterType: 'batch', cmd: process.execPath,
    args: [server, String(port), token], env: {}, scriptHash: sha256(entry),
    healthUrl: `http://127.0.0.1:${port}/`, portHints: [port],
  })
  const fixture = { ...app, folder, token, port, entry, health: `http://127.0.0.1:${port}/`,
    refuse: path.join(folder, 'refuse-stop'), events: path.join(folder, 'events.jsonl') }
  apps.push(fixture)
  return fixture
}
async function buildBackendsAndProbe() {
  console.log('Building current sidecars (2.0.41 / 2.0.42); reusing the compiled production Rust transaction probe...')
  const sources = sourceFiles(path.join(root, 'sidecar')).filter(file => !file.includes(`${path.sep}rundock-install-helper${path.sep}`))
  const before = sourceHashes([...sources, path.join(root, 'sidecar/go.mod'), path.join(root, 'sidecar/go.sum')])
  for (const [version, destination] of [['2.0.41', oldFile], ['2.0.42', newFile]]) {
    await run(go, ['-C', path.join(root, 'sidecar'), 'build', '-trimpath', '-ldflags',
      `-X github.com/launcher-sidecar/internal/api.BuildVersion=${version}`, '-o', destination, './cmd/launcher-sidecar'])
    console.log(`Built backend ${version}: ${destination}`)
  }
  assert.deepEqual(sourceHashes(Object.keys(before).map(file => path.join(root, file))), before, 'Sidecar sources changed during build; rerun')
  const rustSources = [path.join(root, 'src-tauri/examples/update-transaction-probe.rs'), path.join(root, 'src-tauri/src/update_transaction.rs')]
  const rustBefore = sourceHashes(rustSources)
  for (const file of rustSources) assert.ok(fs.statSync(existingProbe).mtimeMs >= fs.statSync(file).mtimeMs,
    'Existing Rust probe is stale; wait for its owner to finish compiling before rerunning')
  fs.copyFileSync(existingProbe, probeFile)
  assert.deepEqual(sourceHashes(rustSources), rustBefore, 'Rust transaction changed while copying probe; rerun')
  console.log('Copied current production Rust transaction probe; no Cargo build performed')
  report.build = { sidecarSources: before, rustSources: rustBefore, oldSha256: sha256(oldFile), newSha256: sha256(newFile), probeSha256: sha256(probeFile) }
  assert.notEqual(report.build.oldSha256, report.build.newSha256)
}
// All fixture checkpoints hold the actual NSIS process alive, preserving the
// production owner PID/creation-time contract while Node checks live services.
const nsiString = value => value.replaceAll('$', '$$').replaceAll('"', '$\\"')
async function buildInstaller() {
  const phaseToken = '${PHASE}'
  const checkpointPrefix = nsiString(directory + path.sep)
  const helper = path.join(directory, 'install-helper.exe')
  const setup = path.join(directory, 'setup.exe')
  const nsi = path.join(directory, 'fixture.nsi')
  const sources = [...sourceFiles(path.join(root, 'sidecar/cmd/rundock-install-helper')), path.join(root, 'src-tauri/windows/installer-hooks.nsh')]
  const before = sourceHashes(sources)
  await run(go, ['-C', path.join(root, 'sidecar'), 'build', '-trimpath', '-ldflags', '-H=windowsgui', '-o', helper, './cmd/rundock-install-helper'])
  fs.writeFileSync(nsi, `Unicode true
RequestExecutionLevel user
SilentInstall silent
!include "LogicLib.nsh"
!define RUNDOCK_INSTALL_HELPER "${nsiString(helper)}"
!include "${nsiString(path.join(root, 'src-tauri/windows/installer-hooks.nsh'))}"
Name "Isolated silent update acceptance"
OutFile "${nsiString(setup)}"
InstallDir "${nsiString(installed)}"
; No registry writes, shortcuts, uninstaller, product launch, or registration.
!macro Checkpoint PHASE
  ClearErrors
  FileOpen $2 "${checkpointPrefix}${phaseToken}.ready" w
  IfErrors checkpoint_error_${phaseToken}
  FileWrite $2 "ready"
  FileClose $2
  StrCpy $3 0
  checkpoint_${phaseToken}:
  IfFileExists "${checkpointPrefix}${phaseToken}.continue" continue_${phaseToken}
  Sleep 100
  IntOp $3 $3 + 1
  IntCmp $3 300 timeout_${phaseToken} checkpoint_${phaseToken} timeout_${phaseToken}
  timeout_${phaseToken}:
  checkpoint_error_${phaseToken}:
  Call RunDockRollbackInstall
  SetErrorLevel 9
  Abort
  continue_${phaseToken}:
!macroend
Section
  !insertmacro NSIS_HOOK_PREINSTALL
  !insertmacro Checkpoint prepared
  SetOutPath $INSTDIR
  ClearErrors
  File /oname=launcher-platform.exe "${nsiString(newFile)}"
  !insertmacro RunDockCheckExtraction
  File /oname=launcher-sidecar.exe "${nsiString(newFile)}"
  !insertmacro RunDockCheckExtraction
  !insertmacro Checkpoint files-written
  !insertmacro NSIS_HOOK_POSTINSTALL
  !insertmacro Checkpoint committed
SectionEnd
`)
  await run(makensis, ['/V2', '/INPUTCHARSET', 'UTF8', nsi])
  assert.deepEqual(sourceHashes(sources), before, 'Helper/hooks changed during build; rerun after owner confirms readiness')
  report.build.installer = { sources: before, helperSha256: sha256(helper), setupSha256: sha256(setup), fixture: fs.readFileSync(nsi, 'utf8') }
  return setup
}
async function liveInstall(setup, baseline) {
  installer = launch(setup, ['/S', '/UPDATE', '/P'], 'installer.log')
  for (const phase of ['prepared', 'files-written', 'committed']) {
    await until(`NSIS ${phase}`, () => {
      if (installer.done) throw new Error(`Installer exited before ${phase}: ${installer.code} ${installer.error || ''}`)
      return fs.existsSync(path.join(directory, `${phase}.ready`))
    })
    const state = await snapshot()
    assert.deepEqual(identities(state), identities(baseline), `${phase}: running identities changed`)
    assert.equal(backend.done, false)
    const files = Object.fromEntries(fs.readdirSync(installed).filter(name => name.endsWith('.exe')).map(name => [name, sha256(path.join(installed, name))]))
    if (phase === 'prepared') {
      assert.equal(fs.existsSync(installedSidecar), false)
      assert.equal(fs.existsSync(installedMain), false)
      assert.equal(Object.values(files).filter(hash => hash === report.build.oldSha256).length, 2)
    } else {
      assert.equal(files['launcher-sidecar.exe'], report.build.newSha256)
      assert.equal(files['launcher-platform.exe'], report.build.newSha256)
    }
    report.observations[phase] = { state, files }
    check(`NSIS ${phase}: original backend/project PID, runId, instance and HTTP 200 preserved`, { backend: state.backend, files })
    fs.writeFileSync(path.join(directory, `${phase}.continue`), 'continue')
  }
  await until('NSIS exit', () => installer.done)
  assert.equal(installer.code, 0)
  assert.deepEqual(identities(await snapshot()), identities(baseline))
  assert.equal(fs.existsSync(path.join(installed, '.rundock-install-backup.json')), false)
}
async function verifyDuplicateRestore(label) {
  const before = await snapshot()
  // Finished journal is a CLI no-op. Also exercise the backend's real restore
  // endpoint on the already running snapshot, rather than testing only the guard.
  await cli('restore')
  for (const project of pending().projects) {
    const response = await raw('/api/desktop/update-restore', 'POST', project)
    assert.ok(response.status === 200 || response.status === 409, JSON.stringify(response))
    if (!pending().error) assert.equal(response.status, 200, 'Successful recovery remains healthy')
    if (response.status === 409) assert.match(response.body.error, /仍在运行|尚未停止/)
  }
  const after = await snapshot()
  for (let index = 0; index < before.projects.length; index++) {
    const a = before.projects[index], b = after.projects[index]
    for (const key of ['pid', 'runId', 'starts']) assert.equal(b[key], a[key], `${label}: ${a.name} ${key}`)
    assert.deepEqual(b.health, a.health)
  }
  check(`${label}: repeated CLI/API restore creates no duplicate project process`, { before: identities(before), after: identities(after) })
}
async function stopRestoreAndRefusal(baseline, stoppedApp) {
  const initialPlan = await api('/api/desktop/update-plan')
  assert.deepEqual(initialPlan.projects.map(project => project.id).sort(), apps.filter(app => app.id !== stoppedApp.id).map(app => app.id).sort())
  const previousBackend = backend
  const stop = await cli('stop', { timeout: 45_000 })
  await until('old backend exited', () => previousBackend.done)
  assert.equal(previousBackend.code, 0)
  assert.equal(pending().phase, 'restoring')
  assert.deepEqual(pending().remaining.map(project => project.id).sort(), initialPlan.projects.map(project => project.id).sort())
  for (const app of apps.filter(app => app.id !== stoppedApp.id)) {
    await assert.rejects(fetch(app.health, { signal: AbortSignal.timeout(1000) }))
    assert.ok(events(app).some(event => event.event === 'signal' && !event.refused))
    assert.ok(events(app).some(event => event.event === 'exit'))
  }
  report.observations.stopped = { durationMs: stop.durationMs, pending: pending(), backendExitCode: previousBackend.code }
  check('Production Rust stop persists exactly the running set and gracefully exits old backend/projects', report.observations.stopped)
  if (prepareOnly) {
    // Preparation-only replacement happens AFTER all processes exit. It cannot
    // establish anything about the live helper; full mode must still be run.
    fs.copyFileSync(newFile, installedSidecar)
    fs.copyFileSync(newFile, installedMain)
  }
  const fresh = await startBackend('2.0.42')
  assert.notEqual(fresh.instanceId, baseline.backend.instanceId)
  await cli('restore', { timeout: 30_000 })
  assert.equal(pending().phase, 'finished')
  assert.equal(pending().error, '')
  assert.deepEqual(pending().remaining, [])
  assert.deepEqual(pending().failures, [])
  for (const app of apps.filter(app => app.id !== stoppedApp.id)) await running(app)
  const restored = await snapshot()
  for (const app of restored.projects.filter(app => app.id !== stoppedApp.id)) {
    const original = baseline.projects.find(project => project.id === app.id)
    assert.notEqual(app.pid, original.pid)
    assert.notEqual(app.runId, original.runId)
    assert.notEqual(app.health.instance, original.health.instance)
    assert.equal(app.starts, 2)
  }
  assert.equal((await view(stoppedApp)).status, 'stopped')
  assert.equal(startCount(stoppedApp), 0)
  report.observations.restored = { state: restored, pending: pending() }
  check('New 2.0.42 backend restores the two HTTP projects; original stopped project never starts', report.observations.restored)
  await verifyDuplicateRestore('Successful update')

  const failurePlan = await api('/api/desktop/update-plan')
  // Force the first restore candidate to refuse stop, proving that recovery
  // continues to the later healthy project after handling the failure.
  const stubborn = apps.find(app => app.id === failurePlan.projects[0].id)
  const healthy = apps.find(app => app.id === failurePlan.projects[1].id)
  fs.writeFileSync(stubborn.refuse, 'refuse graceful stop')
  await api('/api/settings', 'PATCH', { grace_period_seconds: '4' })
  const failureBefore = await snapshot()
  const refused = await cli('stop', { timeout: 25_000, expectedCode: 2 })
  assert.ok(refused.durationMs < 20_000, `Refusal/recovery took ${refused.durationMs}ms; must finish well before the 60s restore polling timeout`)
  const failurePending = pending()
  assert.equal(failurePending.phase, 'finished')
  assert.match(failurePending.error, /未正常退出/)
  assert.equal(backend.done, false)
  assert.equal((await api('/api/health')).instanceId, fresh.instanceId)
  const firstBefore = failureBefore.projects.find(app => app.id === stubborn.id)
  const refusedState = await view(stubborn)
  assert.equal(refusedState.pid, firstBefore.pid)
  assert.equal(refusedState.runId, firstBefore.runId)
  assert.deepEqual(await projectHealth(stubborn), firstBefore.health)
  assert.equal(startCount(stubborn), firstBefore.starts)
  assert.ok(events(stubborn).some(event => event.event === 'signal' && event.refused && event.instance === firstBefore.health.instance))
  assert.equal(events(stubborn).some(event => event.event === 'exit' && event.instance === firstBefore.health.instance), false)
  const healthyAfter = await running(healthy)
  const healthyBefore = failureBefore.projects.find(app => app.id === healthy.id)
  assert.notEqual(healthyAfter.pid, healthyBefore.pid)
  assert.notEqual(healthyAfter.runId, healthyBefore.runId)
  assert.equal(startCount(healthy), healthyBefore.starts + 1)
  assert.equal(failurePending.remaining.some(project => project.id === healthy.id), false)
  assert.ok(failurePending.remaining.every(project => project.id === stubborn.id))
  if (failurePending.remaining.length) assert.ok(failurePending.failures.some(message => message.includes(stubborn.name)))
  assert.equal((await view(stoppedApp)).status, 'stopped')
  assert.equal(startCount(stoppedApp), 0)
  report.observations.refusal = { durationMs: refused.durationMs, deadlineMs: 20_000, stubborn: stubborn.id, healthy: healthy.id, before: failureBefore, after: await snapshot(), pending: failurePending }
  check('Refusing first project survives; Rust recovery promptly finishes and restarts the other project', report.observations.refusal)
  await verifyDuplicateRestore('Cancelled update')
  // The production monitor may recover degraded -> running asynchronously.
  // An explicit retry must not start the surviving process again either.
  await running(stubborn)
  const retryBefore = await snapshot()
  const retryPending = pending()
  retryPending.phase = 'restoring'
  savePending(retryPending)
  await cli('restore', { timeout: 15_000 })
  assert.equal(pending().phase, 'finished')
  assert.deepEqual(pending().remaining, [])
  assert.deepEqual(pending().failures, [])
  assert.match(pending().error, /未正常退出/)
  assert.deepEqual(identities(await snapshot()), identities(retryBefore))
  check('Explicit recovery retry clears remaining failures without restarting surviving/restored projects', { pending: pending() })
}
async function cleanup() {
  const errors = []
  // Release any NSIS checkpoint so its own transaction can finish/roll back.
  for (const phase of ['prepared', 'files-written', 'committed']) fs.writeFileSync(path.join(directory, `${phase}.continue`), 'continue')
  if (installer && !installer.done) {
    try { await until('installer cleanup exit', () => installer.done, 35_000) } catch (error) { errors.push(error.message) }
  }
  for (const app of apps) fs.rmSync(app.refuse, { force: true })
  for (const app of apps) {
    try { if (backend && !backend.done) await action(app, 'stop') } catch (error) { errors.push(error.message) }
    // Fixture-specific authenticated graceful exit also handles backend failure.
    try { await fetch(app.health + '__acceptance/exit', { method: 'POST', headers: { 'x-acceptance-token': app.token }, signal: AbortSignal.timeout(2000) }) } catch {}
  }
  if (backend && !backend.done) {
    try { await api('/api/desktop/shutdown', 'POST', {}) } catch (error) { errors.push(error.message) }
    try { await until('backend cleanup exit', () => backend.done, 10_000) } catch (error) { errors.push(error.message) }
  }
  // Only handles created by this runner; never image-name/port/PID-wide kills.
  for (const child of children.filter(child => !child.done)) {
    child.child.kill()
    await until('owned child exit', () => child.done, 5000)
    errors.push(`Owned child required forced cleanup: ${path.basename(child.file)}`)
  }
  for (const app of apps) {
    await until(`${app.name} HTTP port closed`, async () => {
      try { await fetch(app.health, { signal: AbortSignal.timeout(500) }); return false } catch { return true }
    }, 5000)
  }
  report.fixtureEvents = Object.fromEntries(apps.map(app => [app.id, events(app)]))
  report.logs = Object.fromEntries(fs.readdirSync(directory).filter(name => name.endsWith('.log')).map(name => [name, fs.readFileSync(path.join(directory, name), 'utf8').slice(-100_000)]))
  if (fs.existsSync(reportFile)) report.finalPending = pending()
  // Resolve and validate before recursive removal, including junction escapes.
  const resolvedRoot = fs.realpathSync(temporaryRoot), resolved = fs.realpathSync(directory)
  assert.equal(path.dirname(resolved).toLowerCase(), resolvedRoot.toLowerCase())
  assert.ok(path.basename(resolved).startsWith('silent-update-cycle-'))
  assert.equal(children.every(child => child.done), true)
  fs.rmSync(resolved, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 })
  report.cleanup = { removed: !fs.existsSync(resolved), directory: resolved, allOwnedChildrenExited: true, allProjectPortsClosed: true, errors }
  assert.deepEqual(errors, [], 'Cleanup encountered errors (see report)')
}

try {
  await buildBackendsAndProbe()
  // Preparation intentionally never builds or reads the changing helper.
  const setup = prepareOnly ? null : await buildInstaller()
  backendPort = await freePort()
  base = `http://127.0.0.1:${backendPort}`
  report.backendPort = backendPort
  fs.copyFileSync(oldFile, installedSidecar)
  fs.copyFileSync(oldFile, installedMain) // Valid PE payload only; never executed.
  await startBackend('2.0.41')
  const a = await makeProject('项目 A'), b = await makeProject('项目 B'), stopped = await makeProject('保持停止')
  await action(a, 'start'); await running(a)
  await action(b, 'start'); await running(b)
  report.projectPorts = apps.map(app => app.port)
  const baseline = await snapshot()
  report.observations.baseline = baseline
  check('Old 2.0.41 backend owns two real HTTP projects and one never-started project', identities(baseline))
  if (setup) await liveInstall(setup, baseline)
  else report.pendingCoverage = ['Real NSIS prepare/file/commit while original backend/projects remain running; waiting for helper owner notification']
  await stopRestoreAndRefusal(baseline, stopped)
  report.status = prepareOnly ? 'prepared-awaiting-helper' : 'passed'
  report.passed = prepareOnly ? null : true
} catch (error) {
  failure = error
  report.status = 'failed'
  report.passed = false
  report.error = { message: error.message, stack: error.stack }
} finally {
  try { await cleanup() } catch (error) {
    failure ||= error
    report.status = 'failed'; report.passed = false
    report.cleanup.error = { message: error.message, stack: error.stack }
  }
  report.finishedAt = new Date().toISOString()
  fs.writeFileSync(output, JSON.stringify(report, null, 2))
  console.log(`REPORT: ${output}`)
}
if (failure) { console.error(failure); process.exitCode = 1 }
else console.log(prepareOnly ? 'PREPARED: helper install NOT executed; full acceptance still pending.' : 'PASS: complete isolated silent update cycle; temporary directory cleaned.')
