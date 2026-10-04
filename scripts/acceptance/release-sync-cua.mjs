// Failure inventory precedes implementation: policy loss/restart, project leaks,
// mode switch resets upload, lookalike GitHub URL, missing delivery, wrong POST,
// opening starts work, current-version build unexpectedly uploads.
// Real Git/profile API/SQLite/Vue; intercept only the final external release POST.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { makeFixture, gitAt } from './local-build-fixture.mjs'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const work = mkdtempSync(path.join(tmpdir(), 'rundock-sync-ui-'))
const evidence = path.join(root, 'outputs/acceptance', path.basename(work))
mkdirSync(evidence, { recursive: true })
const fixtures = ['github', 'lookalike'].map(name => makeFixture(work, name, true))
for (const [index, fixture] of fixtures.entries()) {
  gitAt(fixture.root, 'remote', 'add', 'origin', index ? 'https://github.com.example.invalid/fixture/project.git' : 'git@github.com:fixture/project.git')
  const file = path.join(fixture.root, '.launcher/release.yaml')
  const config = JSON.parse(readFileSync(file))
  config.targets = config.targets.filter(target => target.id === 'builtin' || target.id === 'cloud')
  config.targets[0].steps.publish = ''
  writeFileSync(file, JSON.stringify(config, null, 2))
  gitAt(fixture.root, 'add', '.'); gitAt(fixture.root, 'commit', '-m', 'Configure release fixture')
  writeFileSync(path.join(fixture.root, 'source.cjs'), 'module.exports = "updated fixture app";\n')
}
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
async function freePort() { const s = net.createServer(); await new Promise(r => s.listen(0, '127.0.0.1', r)); const p = s.address().port; await new Promise(r => s.close(r)); return p }
const base = `http://127.0.0.1:${await freePort()}`
const uiBase = `http://127.0.0.1:${await freePort()}`
const executable = path.join(root, 'sidecar/.tmp/release-build-ui.exe')
assert.ok(existsSync(executable))
const report = { passed: false, base, uiBase, evidence, boundary: 'Real Git, SQLite, API and Vue. Final release POST captured before external actions; remote delivery separately verified by delivery E2E.' }
let backend, vite, log = '', submission, legacySaveResponses = 0
async function api(route, method = 'GET', body) {
  const response = await fetch(base + route, { method, headers: { Origin: uiBase, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(20000) })
  const value = await response.json(); assert.ok(response.ok, `${route}: ${JSON.stringify(value)}`); return value
}
async function start() {
  backend = spawn(executable, ['-port', base.split(':').at(-1)], { windowsHide: true, cwd: work, env: { ...process.env, LAUNCHER_DATA_DIR: path.join(work, 'data'), LAUNCHER_UI_ORIGIN: uiBase }, stdio: ['ignore', 'pipe', 'pipe'] })
  backend.stdout.on('data', b => { log += b }); backend.stderr.on('data', b => { log += b })
  let healthy = false
  for (let i = 0; i < 100; i++) { try { await api('/api/health'); healthy = true; break } catch { await pause(100) } }
  assert.ok(healthy)
}
async function stop() {
  if (backend?.exitCode === null) { await fetch(base + '/api/desktop/shutdown', { method: 'POST', signal: AbortSignal.timeout(5000) }).catch(() => {}); await pause(1000); if (backend.exitCode === null) backend.kill() }
}
try {
  await start()
  const apps = []
  for (const fixture of fixtures) apps.push(await api('/api/apps', 'POST', { name: path.basename(fixture.root), adapterType: 'batch', entryScript: path.join(fixture.root, 'start.cmd'), cwd: fixture.root }))
  const ui = path.join(root, 'sidecar/.tmp', path.basename(work), 'ui'); mkdirSync(ui, { recursive: true })
  const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
  writeFileSync(path.join(ui, 'index.html'), '<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
  writeFileSync(path.join(ui, 'main.ts'), `import{createApp,h,ref}from'vue';import{createPinia}from'pinia';import Modal from'${source}/components/ReleaseModal.vue';import'${source}/styles.css';window.__LAUNCHER_BASE__=${JSON.stringify(uiBase)};const apps=${JSON.stringify(apps)};createApp({setup(){const selected=ref(-1);return()=>h('div',[...apps.map((app,i)=>h('button',{onClick:()=>selected.value=i},'Open '+app.name)),selected.value<0?h('p','closed'):h(Modal,{key:selected.value,app:apps[selected.value],onClose:()=>selected.value=-1})])}}).use(createPinia()).mount('#app');`)
  const proxy = async (req, res, next) => {
    if (!req.url.startsWith('/api/')) return next()
    try {
      const chunks = []; for await (const chunk of req) chunks.push(chunk)
      const body = Buffer.concat(chunks)
      if (req.method === 'POST' && /\/api\/apps\/[^/]+\/releases$/.test(req.url)) {
        submission = JSON.parse(body); writeFileSync(path.join(evidence, 'submitted-plan.json'), JSON.stringify(submission, null, 2))
        res.writeHead(409, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ code: 'acceptance_capture', error: '验收已记录发布计划，未执行外部上传。' })); return
      }
      const upstream = await fetch(base + req.url, { method: req.method, headers: { Origin: uiBase, 'Content-Type': 'application/json' }, body: ['GET', 'HEAD'].includes(req.method) ? undefined : body, signal: AbortSignal.timeout(120000) })
      let responseBody = Buffer.from(await upstream.arrayBuffer())
      if (req.method === 'PATCH' && req.url.endsWith('/release-profile') && existsSync(path.join(evidence, 'legacy-save.flag'))) {
        // Simulate an old API acknowledging an unknown field without storing it.
        const oldProfile = JSON.parse(responseBody); delete oldProfile.syncPolicy
        responseBody = Buffer.from(JSON.stringify(oldProfile)); legacySaveResponses++
      }
      res.writeHead(upstream.status, { 'Content-Type': upstream.headers.get('Content-Type') || 'application/json' }); res.end(responseBody)
    } catch (error) { res.writeHead(500, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ error: error.message })) }
  }
  vite = await createServer({ configFile: false, root: ui, plugins: [vue(), { name: 'acceptance-api', configureServer(server) { server.middlewares.use(proxy) } }], resolve: { alias: { '@': path.join(root, 'src') } }, optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: Number(uiBase.split(':').at(-1)), strictPort: true, fs: { allow: [root] } } })
  await vite.listen(); console.log(JSON.stringify({ ready: true, uiBase, evidence, apps: apps.map(({id,name}) => ({id,name})) }))
  const receipt = path.join(evidence, 'browser-receipt.json'), deadline = Date.now() + 15 * 60 * 1000
  while (!existsSync(receipt) && Date.now() < deadline) await pause(1000)
  assert.ok(existsSync(receipt)); report.browser = JSON.parse(readFileSync(receipt)); assert.equal(report.browser.passed, true)
  assert.ok(legacySaveResponses > 0, 'Old backend acknowledgement guard exercised')
  report.legacySaveGuardVerified = true
  assert.equal(submission?.buildMode, 'local'); assert.equal(submission.pushRemote, true); assert.equal(submission.createTag, true)
  assert.ok(submission.selectedTargets.some(t => t.targetId === 'builtin' && t.build && t.package && t.publish))
  assert.equal((await api(`/api/apps/${apps[0].id}/release-profile`)).syncPolicy, 'auto')
  assert.equal((await api(`/api/apps/${apps[1].id}/release-profile`)).syncPolicy, 'auto')
  await api(`/api/apps/${apps[0].id}/release-profile`, 'PATCH', { syncPolicy: 'local' })
  await stop(); await start()
  assert.equal((await api(`/api/apps/${apps[0].id}/release-profile`)).syncPolicy, 'local')
  assert.equal((await api(`/api/apps/${apps[1].id}/release-profile`)).syncPolicy, 'auto')
  const invalid = await fetch(base + `/api/apps/${apps[0].id}/release-profile`, { method: 'PATCH', headers: { Origin: uiBase, 'Content-Type': 'application/json' }, body: JSON.stringify({ syncPolicy: 'bad' }) })
  assert.equal(invalid.status, 400)
  report.policySurvivesBackendRestart = true; report.appIsolationVerified = true; report.invalidPolicyRejected = true; report.submission = submission
  for (const app of apps) assert.equal((await api(`/api/apps/${app.id}/releases`)).length, 0, 'No accidental tasks or real external actions')
  report.passed = true
} catch (error) { report.error = error.stack; process.exitCode = 1 }
finally { if (vite) await vite.close(); await stop(); writeFileSync(path.join(evidence, 'backend.log'), log); writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2)); console.log(JSON.stringify(report, null, 2)) }
