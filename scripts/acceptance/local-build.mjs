// End-to-end acceptance: actual Vue, HTTP, SQLite, commands, artifact bytes and restart.
// Failure inventory and boundaries: docs/adr/2026-10-03-pure-local-build.md.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync, existsSync, readdirSync, lstatSync, symlinkSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { createHash, randomUUID } from 'node:crypto'
import { gunzipSync } from 'node:zlib'
import { createRequire } from 'node:module'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { makeFixture, fingerprint, gitAt } from './local-build-fixture.mjs'
import { verifyRealLocalProject } from './local-build-real-project.mjs'
import { removeSealRegistration, recordedSnapshot, prependDiagnosticHistory } from './local-build-recovery-fixture.mjs'
import { verifyLocalBuildInteraction } from './local-build-ui.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const dir = path.join(root, 'sidecar/.tmp', `local-build-${Date.now()}`)
const evidence = path.join(root, 'outputs/acceptance', path.basename(dir))
mkdirSync(evidence, { recursive: true }); mkdirSync(dir, { recursive: true })
// No-Git fixtures must not live inside RunDock's own Git checkout.
const fixtureDir = mkdtempSync(path.join(tmpdir(), 'rundock-acceptance-source-'))
const report = { passed: false, fixtureDirectory: fixtureDir, boundary: 'real isolated sidecar/SQLite + current Vue + real Node bundle/gzip build; not Android/device installation or public release', checks: [] }
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
async function until(check, timeout = 30000) {
  const end = Date.now() + timeout
  while (Date.now() < end) { if (await check()) return; await pause(100) }
  throw new Error('Timed out waiting for local build acceptance')
}
async function freePort() {
  const server = net.createServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port
}
const apiPort = await freePort(), uiPort = await freePort()
const base = `http://127.0.0.1:${apiPort}`, uiBase = `http://127.0.0.1:${uiPort}`
async function response(route, method = 'GET', body, origin = uiBase) {
  const timeout = route.includes('/release/') || route.endsWith('/releases') ? 120000 : 30000
  return fetch(base + route, { method, headers: { 'Content-Type': 'application/json', Origin: origin },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(timeout) })
}
async function request(route, method = 'GET', body) {
  try {
    const res = await response(route, method, body), value = await res.json()
    assert.ok(res.ok, `${method} ${route}: ${JSON.stringify(value)}`); return value
  } catch (error) { throw new Error(`${method} ${route}: ${error.message}`, { cause: error }) }
}
const plain = makeFixture(fixtureDir, 'without-git'), repo = makeFixture(fixtureDir, 'with-git', true)
let backend, vite, browser, context, page, backendLog = ''
const executable = process.env.RUNDOCK_SIDECAR || path.join(root, 'sidecar/.tmp/local-build-validation.exe')
function launch() {
  assert.ok(existsSync(executable), 'Build the acceptance sidecar first')
  const digest = createHash('sha256').update(readFileSync(executable)).digest('hex')
  if (report.sidecar) assert.equal(digest, report.sidecar.sha256, 'Acceptance must not switch binaries between restart checks')
  report.sidecar = { path: executable, sha256: digest }
  backend = spawn(executable, ['-port', String(apiPort)], { windowsHide: true, cwd: dir,
    env: { ...process.env, PATH: path.dirname(process.execPath) + path.delimiter + process.env.PATH,
      LAUNCHER_DATA_DIR: path.join(dir, 'data'), LAUNCHER_UI_ORIGIN: uiBase }, stdio: ['ignore', 'pipe', 'pipe'] })
  backend.stdout.on('data', data => { backendLog += data }); backend.stderr.on('data', data => { backendLog += data })
}
async function healthy() { await until(async () => { try { return (await request('/api/health')).status === 'ok' } catch { return false } }) }
async function stop() {
  if (!backend || backend.exitCode !== null) return
  const exited = new Promise(resolve => backend.once('exit', resolve))
  // This endpoint is native-only; never reuse the browser Origin for shutdown.
  await fetch(base + '/api/desktop/shutdown', { method: 'POST', signal: AbortSignal.timeout(5000) }).catch(() => {})
  await Promise.race([exited, pause(7000)]); if (backend.exitCode === null) backend.kill()
  await pause(300)
}
async function add(project, name) {
  return request('/api/apps', 'POST', { name, entryScript: path.join(project.root, 'start.cmd'), cwd: project.root, adapterType: 'batch' })
}
async function preparation(app) { return (await request(`/api/apps/${app.id}/local-builds`)).preparation }
async function start(app, target = 'good', override = {}) {
  const prep = await preparation(app)
  return request(`/api/apps/${app.id}/local-builds`, 'POST', { requestId: randomUUID(), configFingerprint: prep.configFingerprint, targetIds: [target], ...override })
}
async function settled(run) {
  let view
  await until(async () => { view = await request(`/api/local-builds/${run.id}`); return !['queued', 'running', 'pending'].includes(view.run.status) })
  return view
}
async function settledRelease(run) {
  let view
  await until(async () => { view = await request(`/api/releases/${run.id}`); return !['queued', 'running'].includes(view.run.status) }, 120000)
  return view
}
async function download(view) {
  const artifact = view.artifacts.find(item => item.name.endsWith('.cjs.gz'))
  assert.ok(artifact?.available, JSON.stringify(view))
  const res = await response(`/api/local-builds/${view.run.id}/artifacts/${artifact.id}`)
  assert.equal(res.status, 200); assert.match(res.headers.get('content-disposition') || '', /attachment/)
  const data = Buffer.from(await res.arrayBuffer())
  assert.equal(createHash('sha256').update(data).digest('hex'), artifact.sha256)
  return { artifact, data }
}
function savedFile(name) {
  const found = []
  const walk = folder => {
    for (const child of readdirSync(folder)) {
      const file = path.join(folder, child), info = lstatSync(file)
      if (info.isDirectory()) walk(file)
      else if (child === name) found.push(file)
    }
  }
  walk(path.join(dir, 'data'))
  return found
}
try {
  launch(); await healthy()
  const plainApp = await add(plain, 'No Git fixture'), repoApp = await add(repo, 'Git fixture')
  const beforePlain = fingerprint(plain.root), beforeRepo = fingerprint(repo.root, true)
  const prep = await preparation(plainApp)
  assert.ok(prep.targets.find(t => t.id === 'good')?.available, JSON.stringify(prep))
  assert.ok(!prep.targets.find(t => t.id === 'cloud')?.available)
  assert.equal(prep.targets.find(t => t.id === 'empty')?.available, false)
  const first = await settled(await start(plainApp))
  assert.equal(first.run.status, 'succeeded', JSON.stringify(first))
  assert.equal(first.run.createTag, false); assert.equal(first.run.pushRemote, false)
  assert.equal(first.run.targetVersion, '1.2.3')
  const firstDownload = await download(first)
  assert.match(gunzipSync(firstDownload.data).toString(), /original fixture app/)
  assert.deepEqual(fingerprint(plain.root), beforePlain)
  assert.ok(first.artifacts.length === 2, 'Only final artifact rules are retained, not intermediate bundle')
  report.checks.push('No-Git current-version real build/package/verify/download; source unchanged; final outputs only')
  const longBuilt = await settled(await start(plainApp, 'long-target-'.repeat(5) + 'packages'))
  assert.equal(longBuilt.run.status, 'succeeded', `${longBuilt.run.errorCode}: ${longBuilt.run.errorMessage}`)
  await download(longBuilt)
  assert.ok(savedFile(path.basename(longBuilt.artifacts[0].name)).some(file => file.includes(longBuilt.run.id) && file.length > 260))
  report.checks.push('Windows saved artifact path over 260 characters: atomic save, verified listing and exact download succeed')

  for (const target of ['package-only', 'builtin']) {
    const built = await settled(await start(plainApp, target))
    assert.equal(built.run.status, 'succeeded', JSON.stringify(built)); await download(built)
  }
  report.checks.push('Package-only target works; configured GitHub delivery does not upload in files-only mode')

  const clean = await settled(await start(repoApp)); assert.equal(clean.run.status, 'succeeded')
  assert.deepEqual(fingerprint(repo.root, true), beforeRepo)
  report.checks.push('Clean Git worktree builds without a commit, Tag, version change or push')
  writeFileSync(path.join(repo.root, 'source.cjs'), 'module.exports = "staged current source";\n')
  gitAt(repo.root, 'add', 'source.cjs')
  writeFileSync(path.join(repo.root, 'new-file.txt'), 'untracked current source')
  const beforeDirty = fingerprint(repo.root, true)
  const dirty = await settled(await start(repoApp)); assert.equal(dirty.run.status, 'succeeded')
  assert.match(gunzipSync((await download(dirty)).data).toString(), /staged current source/)
  assert.deepEqual(fingerprint(repo.root, true), beforeDirty)
  report.checks.push('Staged + untracked current source snapshot; original HEAD/index/status/tags unchanged')

  const retryBody = { requestId: randomUUID(), configFingerprint: (await preparation(plainApp)).configFingerprint, targetIds: ['good'] }
  const concurrent = await Promise.all([response(`/api/apps/${plainApp.id}/local-builds`, 'POST', retryBody), response(`/api/apps/${plainApp.id}/local-builds`, 'POST', retryBody)])
  assert.ok(concurrent.some(res => res.ok))
  for (const res of concurrent) assert.ok(res.ok || res.status === 409)
  const same = await Promise.all([request(`/api/apps/${plainApp.id}/local-builds`, 'POST', retryBody), request(`/api/apps/${plainApp.id}/local-builds`, 'POST', retryBody)])
  assert.equal(same[0].id, same[1].id); assert.equal((await settled(same[0])).run.status, 'succeeded')
  report.checks.push('Duplicate submission returns one durable operation')

  const stale = await preparation(plainApp)
  plain.config.targets[0].name = 'Updated fixture target'
  writeFileSync(path.join(plain.root, '.launcher/release.yaml'), JSON.stringify(plain.config))
  assert.equal((await response(`/api/apps/${plainApp.id}/local-builds`, 'POST', { requestId: randomUUID(), configFingerprint: stale.configFingerprint, targetIds: ['good'] })).status, 409)
  for (const body of [{ pushRemote: true }, { targetVersion: '9.9.9' }, { path: 'C:/Windows' }]) {
    const res = await response(`/api/apps/${plainApp.id}/local-builds`, 'POST', { requestId: randomUUID(), configFingerprint: (await preparation(plainApp)).configFingerprint, targetIds: ['good'], ...body })
    assert.ok(res.status >= 400 && res.status < 500)
  }
  for (const target of ['empty', 'cloud']) assert.ok((await response(`/api/apps/${plainApp.id}/local-builds`, 'POST', { requestId: randomUUID(), configFingerprint: (await preparation(plainApp)).configFingerprint, targetIds: [target] })).status >= 400)
  report.checks.push('Stale configuration, remote/version/path injection and unavailable targets rejected')
  for (const target of ['fail', 'missing', 'badverify']) {
    const failed = await settled(await start(plainApp, target))
    assert.equal(failed.run.status, 'failed', JSON.stringify(failed)); assert.ok(failed.run.errorMessage)
    assert.ok(!failed.artifacts.some(item => item.available))
    report.checks.push(`${target}: no false success, actionable error and no unverified downloadable package`)
  }
  const cancelled = await start(plainApp, 'slow')
  await request(`/api/local-builds/${cancelled.id}/cancel`, 'POST', {})
  assert.equal((await settled(cancelled)).run.status, 'cancelled')
  report.checks.push('Immediate cancellation cannot become successful or execute later phases')

  for (const route of [`/api/apps/${plainApp.id}/local-builds`, `/api/local-builds/${first.run.id}`, `/api/local-builds/${first.run.id}/artifacts/${firstDownload.artifact.id}`]) {
    assert.equal((await response(route, 'GET', undefined, 'https://foreign.example')).status, 403)
    assert.equal((await response(route, 'GET', undefined, 'null')).status, 403)
  }
  assert.ok((await response(`/api/local-builds/${dirty.run.id}/artifacts/${firstDownload.artifact.id}`)).status >= 400)
  assert.ok((await response(`/api/local-builds/${first.run.id}/artifacts/../../Windows`)).status >= 400)
  report.checks.push('Foreign/null browser origins, cross-run artifact IDs and path traversal denied')
  for (const suffix of ['', '/cancel', '/sync', '/retry']) {
    assert.equal((await response(`/api/releases/${first.run.id}${suffix}`, suffix ? 'POST' : 'GET', suffix ? {} : undefined, 'https://foreign.example')).status, 403)
  }
  const formalHistory = await request(`/api/apps/${plainApp.id}/releases`)
  assert.ok(!formalHistory.some(run => run.intent === 'build-only' || run.id === first.run.id))
  report.checks.push('Legacy release URLs cannot read/cancel/sync/retry local-only tasks from a foreign origin; formal history stays separate')

  await stop(); launch(); await healthy()
  const reopened = await request(`/api/local-builds/${first.run.id}`)
  assert.equal(reopened.run.status, 'succeeded')
  assert.deepEqual((await download(reopened)).data, firstDownload.data)
  assert.equal((await request(`/api/apps/${plainApp.id}/local-builds`, 'POST', retryBody)).id, same[0].id)
  report.checks.push('Backend restart: history, idempotency and exact saved artifact bytes recover without rebuilding')
  // Deterministic storage fault: simulate atomic bytes already saved but DB registration lost.
  // Only the isolated acceptance database is edited, with the sidecar stopped.
  await stop()
  removeSealRegistration(dir, first.run.id)
  launch(); await healthy()
  const repaired = await request(`/api/local-builds/${first.run.id}`)
  assert.equal(repaired.run.status, 'failed')
  assert.match(repaired.run.errorCode, /interrupted/)
  assert.deepEqual((await download(repaired)).data, firstDownload.data)
  report.checks.push('Injected atomic-save/DB gap: restart re-registers verified captured packages without rerunning commands or claiming task success')
  const incomplete = await settled(await start(plainApp))
  const incompleteFile = incomplete.artifacts.find(file => file.name.endsWith('.sha256'))
  const incompletePath = savedFile(path.basename(incompleteFile.name)).find(file => file.includes(incomplete.run.id))
  await stop()
  removeSealRegistration(dir, incomplete.run.id)
  writeFileSync(incompletePath, 'incomplete saved bytes')
  launch(); await healthy()
  const incompleteView = await request(`/api/local-builds/${incomplete.run.id}`)
  assert.equal(incompleteView.run.status, 'failed')
  assert.ok(!incompleteView.artifacts.some(file => file.available))
  report.checks.push('Injected incomplete-save/DB gap: restart cannot register or expose partially valid saved files')

  const tamperRun = await settled(await start(plainApp))
  const { artifact: tamperArtifact } = await download(tamperRun)
  const stored = savedFile(path.basename(tamperArtifact.name)).find(file => file.includes(tamperRun.run.id))
  assert.ok(stored, 'Saved output has a readable original filename in the task directory')
  const storedBytes = readFileSync(stored); writeFileSync(stored, Buffer.concat([storedBytes, Buffer.from('changed')]))
  assert.equal((await request(`/api/local-builds/${tamperRun.run.id}`)).artifacts.find(item => item.id === tamperArtifact.id).available, false)
  assert.ok((await response(`/api/local-builds/${tamperRun.run.id}/artifacts/${tamperArtifact.id}`)).status >= 400)
  writeFileSync(stored, storedBytes)
  report.checks.push('Saved artifact tampering becomes unavailable and download is blocked, not silently served')

  const linked = makeFixture(fixtureDir, 'linked-source')
  symlinkSync(repo.root, path.join(linked.root, 'outside-source'), 'junction')
  const linkedApp = await add(linked, 'Linked source fixture'), linkedRun = await settled(await start(linkedApp))
  assert.equal(linkedRun.run.status, 'failed'); assert.ok(!linkedRun.artifacts.some(item => item.available))
  report.checks.push('Windows source junction cannot escape the project into another source tree')

  const interrupted = await start(plainApp, 'slow')
  await until(async () => (await request(`/api/local-builds/${interrupted.id}`)).run.stage.includes('build'))
  const interruptedSnapshot = recordedSnapshot(dir, interrupted.id)
  assert.ok(interruptedSnapshot && existsSync(interruptedSnapshot))
  await stop(); launch(); await healthy()
  const interruptedView = await request(`/api/local-builds/${interrupted.id}`)
  assert.ok(['failed', 'interrupted', 'cancelled'].includes(interruptedView.run.status), JSON.stringify(interruptedView))
  assert.match(interruptedView.run.errorCode, /interrupted|cancel/)
  await pause(300)
  assert.equal((await request(`/api/local-builds/${interrupted.id}`)).run.status, interruptedView.run.status)
  assert.equal(existsSync(interruptedSnapshot), false, 'Recorded interrupted build snapshot is cleaned')
  report.checks.push('Interrupted task is explicit after restart and is never automatically rebuilt or published')

  const formal = makeFixture(fixtureDir, 'formal-local', true), formalApp = await add(formal, 'Formal local fixture')
  writeFileSync(path.join(formal.root, 'source.cjs'), 'module.exports = "formal committed fixture";\n')
  const formalPreflight = await request(`/api/apps/${formalApp.id}/release/preflight`, 'POST')
  const formalBody = { intent: 'formal', skipChecks: true, statusFingerprint: formalPreflight.statusFingerprint,
    selectedPaths: ['source.cjs'], selectedTargets: [{ targetId: 'good', build: true, package: true, publish: false, deploy: false }],
    versionMode: 'unchanged', buildMode: 'local', createTag: false, pushRemote: false, targetVersion: '1.2.3',
    versions: [], manualDecisions: [], sensitiveExceptions: [] }
  const candidate = await request(`/api/apps/${formalApp.id}/release/candidate`, 'POST', formalBody)
  const formalRun = await request(`/api/apps/${formalApp.id}/releases`, 'POST', { ...formalBody, candidateId: candidate.id, commitMessage: 'Acceptance formal local build', externalActionsConfirmed: true })
  const formalView = await settledRelease(formalRun)
  assert.equal(formalView.run.status, 'succeeded', JSON.stringify(formalView))
  const formalSaved = await request(`/api/releases/${formalRun.id}/saved-artifacts`)
  assert.equal(formalSaved.artifacts.length, 2); assert.ok(formalSaved.artifacts.every(file => file.available))
  const formalFile = formalSaved.artifacts.find(file => file.name.endsWith('.cjs.gz'))
  const formalRes = await response(`/api/releases/${formalRun.id}/artifacts/${formalFile.id}`)
  assert.equal(formalRes.status, 200)
  assert.match(gunzipSync(Buffer.from(await formalRes.arrayBuffer())).toString(), /formal committed fixture/)
  assert.equal(gitAt(formal.root, 'log', '-1', '--format=%s'), 'Acceptance formal local build')
  assert.equal(gitAt(formal.root, 'tag', '--list'), 'v1.2.3')
  report.checks.push('Existing formal local-only release still commits intentionally and now reliably saves/downloads final packages')
  await stop()
  removeSealRegistration(dir, formalRun.id, 'formal')
  launch(); await healthy()
  const recoveredFormal = await request(`/api/releases/${formalRun.id}/saved-artifacts`)
  assert.ok(recoveredFormal.artifacts.length === 2 && recoveredFormal.artifacts.every(file => file.available))
  assert.equal((await request(`/api/releases/${formalRun.id}`)).run.errorCode, 'release_interrupted')
  assert.equal(gitAt(formal.root, 'log', '-1', '--format=%s'), 'Acceptance formal local build')
  report.checks.push('Formal local no-upload storage gap: restart restores verified final files without recommitting, rebuilding or publishing')
  writeFileSync(path.join(formal.root, 'source.cjs'), 'module.exports = "checked formal current version";\n')
  const checkedBody = { ...formalBody, skipChecks: false,
    statusFingerprint: (await request(`/api/apps/${formalApp.id}/release/preflight`, 'POST')).statusFingerprint }
  delete checkedBody.targetVersion
  const checkedCandidate = await request(`/api/apps/${formalApp.id}/release/candidate`, 'POST', checkedBody)
  const checks = await request(`/api/apps/${formalApp.id}/release/candidate/check`, 'POST', { candidateId: checkedCandidate.id })
  assert.equal(checks.accepted, true, JSON.stringify(checks))
  assert.ok(checks.checkResults.some(result => result.id === 'target:good' && result.status === 'passed'))
  const checkedRun = await request(`/api/apps/${formalApp.id}/releases`, 'POST', { ...checkedBody,
    candidateId: checkedCandidate.id, commitMessage: 'Acceptance checked current version', externalActionsConfirmed: true })
  assert.equal((await settledRelease(checkedRun)).run.status, 'succeeded')
  assert.ok((await request(`/api/releases/${checkedRun.id}/saved-artifacts`)).artifacts.every(file => file.available && file.name.includes('1.2.3')))
  assert.equal(JSON.parse(readFileSync(path.join(formal.root, 'package.json'))).version, '1.2.3')
  report.checks.push('Check-enabled untagged formal build reads actual candidate version 1.2.3, not stale configured 0.0.1 or a suggested bump')

  const harness = path.join(dir, 'ui'); mkdirSync(harness)
  const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
  writeFileSync(path.join(harness, 'index.html'), '<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
  writeFileSync(path.join(harness, 'main.ts'), `import{createApp,h,ref}from'vue';import{createPinia}from'pinia';import Modal from'${source}/components/ReleaseModal.vue';import'${source}/styles.css';window.__LAUNCHER_BASE__=${JSON.stringify(base)};createApp({setup(){const a=ref(${JSON.stringify(plainApp)}),shown=ref(true);window.reopenBuild=()=>{shown.value=true};return()=>h('div',[h('button',{onClick:()=>shown.value=true},'Open build'),shown.value?h(Modal,{app:a.value,onClose:()=>shown.value=false}):h('p','closed')])}}).use(createPinia()).mount('#app');`)
  vite = await createServer({ configFile: false, root: harness, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } },
    optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: uiPort, strictPort: true, fs: { allow: [root] } } })
  await vite.listen()
  const require = createRequire(import.meta.url), { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
  const browserOptions = process.env.RUNDOCK_BROWSER_EXECUTABLE
    ? { executablePath: process.env.RUNDOCK_BROWSER_EXECUTABLE }
    : { channel: process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge' }
  report.browser = browserOptions
  browser = await chromium.launch({ ...browserOptions, headless: true })
  context = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1200, height: 960 }, acceptDownloads: true })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage(); const pageErrors = []
  page.on('pageerror', error => pageErrors.push(error.message))
  await page.goto(uiBase)
  await page.getByRole('tab', { name: '本地构建', exact: true }).click()
  await page.getByText('在本机生成安装包，不提交代码、不创建版本、不上传。', { exact: true }).waitFor()
  await page.screenshot({ path: path.join(evidence, 'local-build-ready.png'), fullPage: true })
  const button = page.getByRole('button', { name: '开始构建', exact: true })
  await page.getByRole('checkbox', { name: /Updated fixture target/ }).check()
  await button.click()
  await page.locator('#release-panel-local-build .local-build-status').filter({ hasText: '本地构建完成' }).waitFor({ timeout: 30000 })
  await page.getByRole('button', { name: '打开产物目录', exact: true }).waitFor()
  const artifactPattern = `${base}/api/local-builds/*/artifacts/*`
  await page.route(artifactPattern, route => route.abort('failed'))
  await page.getByRole('button', { name: /^下载 / }).first().click()
  await page.getByText('下载失败，请重试；也可在已保存目录中取文件。', { exact: true }).waitFor()
  assert.ok(await page.getByRole('button', { name: '打开产物目录', exact: true }).isEnabled())
  await page.unroute(artifactPattern)
  report.checks.push('Browser file fetch interrupted: clear retry/manual saved-directory fallback, without losing verified task outputs')
  const downloaded = page.waitForEvent('download')
  await page.getByRole('button', { name: /^下载 / }).first().click()
  const browserDownload = await downloaded
  await browserDownload.saveAs(path.join(evidence, browserDownload.suggestedFilename()))
  assert.ok(browserDownload.suggestedFilename().includes('1.2.3'))
  await page.screenshot({ path: path.join(evidence, 'local-build-complete.png'), fullPage: true })
  if (process.env.RUNDOCK_ACCEPTANCE_OPEN_FOLDER === '1') {
    await page.getByRole('button', { name: '打开产物目录', exact: true }).click()
    await page.getByText('已打开产物目录', { exact: true }).waitFor()
    report.checks.push('Actual App button invokes verified native output-directory opening')
  }
  const viewedId = await page.evaluate(() => Object.entries(localStorage).find(([key]) => key.startsWith('rundock.local-build.view.'))?.[1])
  const viewed = await request(`/api/local-builds/${viewedId}`)
  const damagedArtifact = viewed.artifacts.find(file => file.name.endsWith('.sha256'))
  const damagedPath = savedFile(path.basename(damagedArtifact.name)).find(file => file.includes(viewedId))
  const originalArtifact = readFileSync(damagedPath)
  writeFileSync(damagedPath, 'damaged checksum file')
  await page.reload()
  await page.getByRole('tab', { name: '本地构建', exact: true }).click()
  await page.getByText('有产物不可用，无法打开目录；可用文件仍可下载。', { exact: true }).waitFor()
  assert.ok(await page.getByRole('button', { name: '打开产物目录', exact: true }).isDisabled())
  assert.equal(await page.getByRole('button', { name: /^下载 / }).count(), 1)
  writeFileSync(damagedPath, originalArtifact)
  report.checks.push('Partial artifact tampering: App disables directory opening, explains why, and keeps verified individual download')
  await verifyLocalBuildInteraction({ page, context, base, uiBase, evidence, report,
    prepareLongHistory: async runId => { await stop(); prependDiagnosticHistory(dir, runId); launch(); await healthy() } })
  const manifestPath = path.join(plain.root, '.launcher/release.yaml'), manifestBytes = readFileSync(manifestPath)
  writeFileSync(manifestPath, '{invalid config')
  const unavailablePrep = await request(`/api/apps/${plainApp.id}/local-builds`)
  assert.equal(unavailablePrep.preparation, null)
  assert.ok(unavailablePrep.preparationError && unavailablePrep.recentRuns.length)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByRole('tab', { name: '本地构建', exact: true }).click()
  const localPanel = page.locator('#release-panel-local-build')
  await localPanel.getByRole('button', { name: '重新读取配置', exact: true }).waitFor()
  await localPanel.locator('.local-build-history button').first().click()
  await localPanel.getByRole('button', { name: /^下载 / }).first().waitFor()
  writeFileSync(manifestPath, manifestBytes)
  report.checks.push('Broken current configuration + fresh browser storage: old verified tasks and downloads remain accessible')
  assert.deepEqual(pageErrors, [])
  report.checks.push('Actual ReleaseModal no-Git tab, concise in-App help, target selection, real build success and browser download')
  assert.ok(!existsSync(path.join(plain.root, 'uploaded.txt')) && !existsSync(path.join(plain.root, 'deployed.txt')))
  assert.deepEqual(fingerprint(repo.root, true), beforeDirty)
  await verifyRealLocalProject({ request, response, evidence, report, pause })
  report.passed = true
} catch (error) {
  report.error = error.stack || String(error)
  if (page) await page.screenshot({ path: path.join(evidence, 'failure.png'), fullPage: true }).catch(() => {})
  process.exitCode = 1
} finally {
  if (context) await context.tracing.stop({ path: path.join(evidence, 'trace.zip') }).catch(() => {})
  if (browser) await browser.close().catch(() => {})
  if (vite) await vite.close().catch(() => {})
  await stop()
  writeFileSync(path.join(evidence, 'backend.log'), backendLog)
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify({ ...report, evidence }, null, 2))
}
