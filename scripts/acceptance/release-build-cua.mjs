// Real isolated service + current Vue; operate only through Codex's in-app browser.
// Failure inventory: wrong default, third tab, auto-start on mode change, lost task
// on Settings/close/reopen, no-Git build blocked, missing or corrupt saved outputs.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import net from 'node:net'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
import { makeFixture, fingerprint } from './local-build-fixture.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const work = mkdtempSync(path.join(tmpdir(), 'rundock-release-ui-'))
const evidence = path.join(root, 'outputs/acceptance', path.basename(work))
mkdirSync(evidence, { recursive: true })
const fixture = makeFixture(work, 'no-git-build')
const before = fingerprint(fixture.root)
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}
const base = `http://127.0.0.1:${await freePort()}`
const uiBase = `http://127.0.0.1:${await freePort()}`
const executable = path.join(root, 'sidecar/.tmp/release-build-ui.exe')
assert.ok(existsSync(executable), 'Build the isolated acceptance sidecar first')
let backend, vite, log = ''
const report = { passed: false, base, uiBase, evidence, sourceDirectory: fixture.root,
  boundary: 'Isolated no-Git fixture; real API, SQLite, Vue and configured build commands; CUA browser only.' }
async function api(route, method = 'GET', body) {
  const response = await fetch(base + route, { method, headers: { Origin: uiBase, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000) })
  const value = await response.json()
  assert.ok(response.ok, `${route}: ${JSON.stringify(value)}`)
  return value
}
try {
  backend = spawn(executable, ['-port', base.split(':').at(-1)], { windowsHide: true, cwd: work,
    env: { ...process.env, LAUNCHER_DATA_DIR: path.join(work, 'data'), LAUNCHER_UI_ORIGIN: uiBase }, stdio: ['ignore', 'pipe', 'pipe'] })
  backend.stdout.on('data', bytes => { log += bytes }); backend.stderr.on('data', bytes => { log += bytes })
  let healthy = false
  for (let attempt = 0; attempt < 100; attempt++) {
    try { await api('/api/health'); healthy = true; break } catch { await pause(100) }
  }
  assert.ok(healthy, 'Isolated backend started')
  const app = await api('/api/apps', 'POST', { name: 'Release UI acceptance', adapterType: 'batch',
    entryScript: path.join(fixture.root, 'start.cmd'), cwd: fixture.root })
  const ui = path.join(root, 'sidecar/.tmp', path.basename(work), 'ui'); mkdirSync(ui, { recursive: true })
  const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
  writeFileSync(path.join(ui, 'index.html'), '<html lang="zh-CN"><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
  writeFileSync(path.join(ui, 'main.ts'), `import{createApp,h,ref}from'vue';import{createPinia}from'pinia';import Modal from'${source}/components/ReleaseModal.vue';import'${source}/styles.css';window.__LAUNCHER_BASE__=${JSON.stringify(base)};createApp({setup(){const shown=ref(true);return()=>h('div',[h('button',{onClick:()=>shown.value=true},'Open build'),shown.value?h(Modal,{app:${JSON.stringify(app)},onClose:()=>shown.value=false}):h('p','closed')])}}).use(createPinia()).mount('#app');`)
  vite = await createServer({ configFile: false, root: ui, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } },
    optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: Number(uiBase.split(':').at(-1)), strictPort: true, fs: { allow: [root] } } })
  await vite.listen()
  console.log(JSON.stringify({ ready: true, uiBase, evidence, appId: app.id }))
  const receipt = path.join(evidence, 'browser-receipt.json')
  const deadline = Date.now() + 10 * 60 * 1000
  while (!existsSync(receipt) && Date.now() < deadline) await pause(1000)
  assert.ok(existsSync(receipt), 'CUA browser receipt received within 10 minutes')
  report.browser = JSON.parse(readFileSync(receipt))
  assert.equal(report.browser.passed, true)
  const history = (await api(`/api/apps/${app.id}/local-builds`)).recentRuns
  assert.equal(history.length, 1, 'Switching views did not start extra tasks')
  const view = await api(`/api/local-builds/${history[0].id}`)
  assert.equal(view.run.appId, app.id)
  assert.equal(view.run.status, 'succeeded')
  assert.ok(view.artifacts.length === 2 && view.artifacts.every(file => file.available))
  const after = fingerprint(fixture.root)
  for (const [file, hash] of Object.entries(before.files))
    assert.equal(after.files[file], hash, `Source/config changed: ${file}`)
  // Opening the formal release dialog diagnoses its no-Git prerequisite. Those
  // explicit diagnostic files are not source; no other new files are allowed.
  const added = Object.keys(after.files).filter(file => !(file in before.files))
  assert.ok(added.every(file => /^\.launcher[\\/]diagnostics[\\/](README-AI\.md|latest\.json|events-\d{4}-\d{2}-\d{2}\.jsonl)$/.test(file)))
  report.sourceVerification = { originalFilesUnchanged: true, addedDiagnostics: added }
  report.run = view
  report.passed = true
} catch (error) { report.error = error.stack; process.exitCode = 1 }
finally {
  if (vite) await vite.close()
  // Only this script's isolated service, never the user's live development backend.
  if (backend && backend.exitCode === null) {
    await fetch(base + '/api/desktop/shutdown', { method: 'POST', signal: AbortSignal.timeout(5000) }).catch(() => {})
    await pause(1000)
    if (backend.exitCode === null) backend.kill()
  }
  writeFileSync(path.join(evidence, 'backend.log'), log)
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify(report, null, 2))
}
