// Real RunDock installers through an isolated sidecar. No upload or installation.
import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { createHash, randomUUID } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, copyFileSync, existsSync, readdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const evidence = path.join(root, 'outputs/acceptance', `release-hash-e2e-${Date.now()}`)
mkdirSync(evidence, { recursive: true })
const tempBase = tmpdir()
mkdirSync(tempBase, { recursive: true })
const work = mkdtempSync(path.join(tempBase, 'rh-e2e-'))
const project = path.join(work, 'project'), data = path.join(work, 'data'), scratch = path.join(work, 'scratch')
for (const dir of [project, data, scratch]) mkdirSync(dir)
const ps = path.join(process.env.SystemRoot || 'C:/Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe')
const env = { ...process.env, GOPATH: path.join(process.env.USERPROFILE, 'gopath'), CARGO_BUILD_JOBS: '2', GH_TOKEN: '', GITHUB_TOKEN: '', GH_ENTERPRISE_TOKEN: '', GITHUB_ENTERPRISE_TOKEN: '', GIT_TERMINAL_PROMPT: '0' }
const report = { passed: false, boundary: 'isolated actual HTTP build, installers, verification and downloads; no GitHub upload, no installation', evidence, checks: [] }
const json = (name, value) => writeFileSync(path.join(evidence, name), JSON.stringify(value, null, 2))
const digest = file => createHash('sha256').update(readFileSync(file)).digest('hex')
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
function command(exe, args, cwd = root) {
  return execFileSync(exe, args, { cwd, env, encoding: 'utf8', windowsHide: true, timeout: 180000, maxBuffer: 8 * 1024 * 1024 })
}
const git = (...args) => command('git', ['-C', project, '-c', 'core.hooksPath=', ...args]).trim()
const originalStatus = command('git', ['status', '--porcelain=v1'])
function listeners() {
  return JSON.parse(command(ps, ['-NoProfile', '-Command', "$ports=17654,17655,17656,17618,11784,8080; @(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | Where-Object { $_.LocalPort -in $ports } | ForEach-Object { $p=Get-Process -Id $_.OwningProcess -ErrorAction Stop; [pscustomobject]@{port=$_.LocalPort;address=$_.LocalAddress;pid=$p.Id;created=$p.StartTime.ToUniversalTime().ToString('o')} } | Sort-Object port,address) | ConvertTo-Json -Compress"]))
}
let child, base, backendLog = '', launchNumber = 0
async function request(route, method = 'GET', body) {
  const response = await fetch(base + route, { method, headers: { Origin: 'http://127.0.0.1:1421', 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000) })
  const value = await response.json()
  assert.ok(response.ok, `${route}: ${JSON.stringify(value)}`)
  return value
}
async function stop() {
  if (!child) return
  const current = child
  const alive = () => current.exitCode === null && current.signalCode === null
  if (base && alive()) await fetch(base + '/api/desktop/shutdown', { method: 'POST', signal: AbortSignal.timeout(5000) }).catch(() => {})
  for (let i = 0; i < 70 && alive(); i++) await pause(100)
  if (alive()) { current.kill(); for (let i = 0; i < 30 && alive(); i++) await pause(100) }
  assert.equal(alive(), false, 'Isolated backend did not exit')
  writeFileSync(path.join(evidence, `backend-${launchNumber}.log`), backendLog)
  child = null; base = null
}
async function start(executable) {
  backendLog = ''; launchNumber++
  const portFile = path.join(data, 'sidecar.port')
  if (existsSync(portFile)) rmSync(portFile)
  child = spawn(executable, ['-addr', '127.0.0.1:0'], { cwd: work, windowsHide: true, env: { ...env, APPDATA: data, LAUNCHER_DATA_DIR: data, LAUNCHER_UI_ORIGIN: 'http://127.0.0.1:1421', TMP: scratch, TEMP: scratch }, stdio: ['ignore', 'pipe', 'pipe'] })
  let launchError
  child.on('error', error => { launchError = error })
  child.stdout.on('data', b => { backendLog += b }); child.stderr.on('data', b => { backendLog += b })
  for (let i = 0; i < 300; i++) {
    if (launchError) throw launchError
    assert.equal(child.exitCode, null, backendLog)
    if (existsSync(portFile)) {
      base = `http://127.0.0.1:${readFileSync(portFile, 'utf8').trim()}`
      try { if ((await request('/api/health')).status === 'ok') return } catch {}
    }
    await pause(100)
  }
  throw Error('Isolated backend startup timed out')
}
try {
  report.beforeListeners = listeners()
  report.baseCommit = command('git', ['rev-parse', 'HEAD']).trim()
  const archive = path.join(work, 'source.tar')
  command('git', ['archive', '--format=tar', '-o', archive, 'HEAD'])
  command('tar.exe', ['-xf', archive, '-C', project])
  // HEAD contains the actual failed v2.0.45 source. Overlay only the reviewed fix,
  // leaving the user's five dirty version files untouched.
  for (const file of ['.launcher/release.yaml', 'scripts/release-build.ps1', 'scripts/release-hash.ps1', 'scripts/verify-release-assets.ps1', 'sidecar/internal/publisher/target_failure.go', 'sidecar/internal/publisher/target_failure_test.go']) {
    copyFileSync(path.join(root, file), path.join(project, file))
  }
  report.version = JSON.parse(readFileSync(path.join(project, 'package.json'))).version
  assert.equal(report.version, '2.0.45', 'Review the intended test version before using a different HEAD')
  const configFile = path.join(project, '.launcher/release.yaml')
  const config = JSON.parse(readFileSync(configFile, 'utf8'))
  const target = config.targets.find(t => t.id === 'local-windows')
  assert.ok(target)
  // Fault injection only in the fixture: the obsolete command must never run.
  writeFileSync(path.join(project, 'scripts/e2e-build.ps1'), `param([string]$Version,[string]$Commit)
$ErrorActionPreference='Stop'
function Get-FileHash { throw 'E2E: Get-FileHash unavailable' }
& "$PSScriptRoot/release-build.ps1" -TagName "v$Version" -SourceCommit $Commit -OutputDirectory .tmp/release-assets -InstallDependencies -SkipTests
`)
  target.steps.build = 'powershell -NoProfile -ExecutionPolicy Bypass -File scripts/e2e-build.ps1 -Version ${VERSION} -Commit ${COMMIT_SHA}'
  config.targets = [target]
  delete target.delivery; delete target.steps.publish; delete target.steps.deploy; delete config.automation
  writeFileSync(configFile, JSON.stringify(config, null, 2))
  git('init', '-b', 'acceptance'); git('config', 'core.autocrlf', 'false')
  git('add', '-A'); git('-c', 'user.name=Local acceptance', '-c', 'user.email=acceptance@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-m', 'Isolated v2.0.45 hash-fix acceptance')
  report.fixtureCommit = git('rev-parse', 'HEAD')
  assert.equal(git('remote'), '')
  const executable = path.join(work, 'sidecar.exe')
  console.log('Compiling isolated backend; evidence:', evidence)
  command(path.join(process.env.USERPROFILE, 'go/bin/go.exe'), ['build', '-o', executable, './cmd/launcher-sidecar'], path.join(root, 'sidecar'))
  await start(executable)
  const app = await request('/api/apps', 'POST', { name: 'RunDock hash E2E', entryScript: path.join(project, 'scripts/dev.bat'), cwd: project, adapterType: 'batch' })
  const prep = await request(`/api/apps/${app.id}/local-builds`)
  json('preparation.json', prep)
  assert.ok(prep.preparation, prep.preparationError)
  const body = { requestId: randomUUID(), configFingerprint: prep.preparation.configFingerprint, targetIds: ['local-windows'] }
  const run = await request(`/api/apps/${app.id}/local-builds`, 'POST', body)
  report.runId = run.id
  assert.equal((await request(`/api/apps/${app.id}/local-builds`, 'POST', body)).id, run.id)
  report.checks.push('Duplicate click while running returns the same build')
  let view, stage
  for (const deadline = Date.now() + 20 * 60 * 1000; Date.now() < deadline;) {
    view = await request(`/api/local-builds/${run.id}`)
    json('latest-build.json', view)
    if (view.run.stage !== stage) { stage = view.run.stage; console.log('Build stage:', stage) }
    if (!['queued', 'pending', 'running'].includes(view.run.status)) break
    await pause(3000)
  }
  assert.equal(view.run.status, 'succeeded', JSON.stringify(view))
  assert.equal(view.run.pushRemote, false); assert.equal(view.run.createTag, false)
  assert.ok(view.targets.every(t => t.status === 'succeeded'))
  report.checks.push('Actual NSIS and MSI build, target verification, persistent artifact storage succeeded without Get-FileHash')
  const packages = path.join(evidence, 'packages'); mkdirSync(packages)
  for (const artifact of view.artifacts) {
    assert.equal(artifact.available, true)
    const response = await fetch(`${base}/api/local-builds/${run.id}/artifacts/${artifact.id}`, { headers: { Origin: 'http://127.0.0.1:1421' }, signal: AbortSignal.timeout(30000) })
    assert.equal(response.status, 200)
    const file = path.join(packages, path.basename(artifact.name))
    writeFileSync(file, Buffer.from(await response.arrayBuffer()))
    assert.equal(digest(file), artifact.sha256)
  }
  report.artifacts = view.artifacts
  assert.equal(view.artifacts.length, 4)
  report.checks.push('All four files downloaded through HTTP match stored SHA-256')
  const verifyArgs = ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', path.join(root, 'scripts/verify-release-assets.ps1'), '-Version', report.version, '-Commit', report.fixtureCommit, '-Directory', packages, '-SignaturePolicy', 'Unsigned']
  writeFileSync(path.join(evidence, 'verification.log'), command(ps, verifyArgs))
  report.checks.push('Downloaded EXE/MSI embedded version, x64, manifest source, SHA-256 and existing unsigned policy verified')
  await stop(); await start(executable)
  assert.equal((await request(`/api/apps/${app.id}/local-builds`, 'POST', body)).id, run.id)
  const restored = await request(`/api/local-builds/${run.id}`)
  assert.equal(restored.run.status, 'succeeded'); assert.ok(restored.artifacts.every(a => a.available))
  report.checks.push('Backend restart preserves the same build and verified artifacts without rebuilding')
  await stop()
  assert.deepEqual(readdirSync(scratch).filter(n => /^(rundock-local-build-|rd-t-)/.test(n)), [])
  report.checks.push('Source snapshot and per-command scratch directories automatically removed')
  const exe = path.join(packages, `RunDock_${report.version}_x64-setup.exe`)
  const original = readFileSync(exe), changed = Buffer.from(original); changed[changed.length - 1] ^= 1
  writeFileSync(exe, changed)
  try {
    let rejected = false
    try { command(ps, verifyArgs) } catch (error) { rejected = /Installer hash mismatch/.test(String(error.stderr)); writeFileSync(path.join(evidence, 'tamper-rejection.log'), String(error.stderr)) }
    assert.ok(rejected, 'Tampered installer must be rejected')
  } finally { writeFileSync(exe, original) }
  report.checks.push('Modified installer is rejected; original bytes restored')
  assert.equal(git('status', '--porcelain=v1'), '')
  assert.equal(command('git', ['status', '--porcelain=v1']), originalStatus)
  report.afterListeners = listeners(); assert.deepEqual(report.afterListeners, report.beforeListeners)
  report.checks.push('Source workspace unchanged; existing listener PIDs and creation times unchanged')
  report.passed = true
} catch (error) { report.error = error.stack || String(error); process.exitCode = 1 }
finally {
  try { await stop() } catch (error) { report.cleanupError = String(error); report.passed = false; process.exitCode = 1 }
  // Only the unique fixture tree can be removed; final packages and logs are outside it.
  const resolved = path.resolve(work)
  assert.ok(resolved.startsWith(path.resolve(tempBase) + path.sep) && path.basename(resolved).startsWith('rh-e2e-'))
  if (!child) { rmSync(resolved, { recursive: true, force: true, maxRetries: 5, retryDelay: 500 }); report.fixtureRemoved = !existsSync(resolved) }
  json('report.json', report)
  console.log(JSON.stringify(report, null, 2))
}
