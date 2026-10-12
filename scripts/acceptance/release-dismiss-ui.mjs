// Actual Vue dialog and close handlers; isolated mock APIs, no live publishing.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const fixture = path.join(root, '.tmp', `release-dismiss-${Date.now()}`)
const evidence = path.join(root, 'outputs/acceptance/release-dismiss')
mkdirSync(fixture, { recursive: true }); mkdirSync(evidence, { recursive: true })
const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
writeFileSync(path.join(fixture, 'index.html'), '<html lang="zh"><meta charset="utf-8"><div id="app"></div><script type="module" src="/main.ts"></script></html>')
writeFileSync(path.join(fixture, 'main.ts'), `
import {createApp,h,ref,nextTick} from 'vue';import Modal from '${source}/components/ReleaseModal.vue';
import {setLocale} from '${source}/i18n/index.ts';import '${source}/styles.css';
window.__LAUNCHER_BASE__='http://dismiss.fixture.invalid';setLocale('zh-CN');
createApp({setup(){const visible=ref(true),panel=ref();
window.reopenFixture=async()=>{visible.value=false;await nextTick();localStorage.clear();visible.value=true};
window.phaseFixture=async(phase)=>{for(const [name,state] of Object.entries(panel.value.testState))state.value=name===phase;await nextTick()};
return()=>visible.value?h(Modal,{ref:panel,app:{id:'dismiss-ui',name:'RunDock',status:'stopped'},onClose:()=>visible.value=false}):null}}).mount('#app');
`)
const config = { ...JSON.parse(readFileSync(path.join(root, '.launcher/release.yaml'), 'utf8')), source: 'file', warnings: [] }
const profile = { appId: 'dismiss-ui', buildMode: 'local', syncPolicy: 'auto', remoteName: 'origin', versionStrategy: 'tauri', preReleaseCommand: '', createTag: true, versionMode: 'auto' }
const preflight = { repoRoot: 'C:/fixture', branch: 'master', headSha: 'a'.repeat(40), remoteName: 'origin', remoteUrl: 'https://github.com/fixture/project',
  remotes: ['origin'], latestTag: 'v2.0.30', suggestedVersion: '2.0.31', suggestedVersions: { product: '2.0.31' }, latestGroupTags: {}, commitsSinceTags: {},
  versionStrategy: 'tauri', versionFiles: ['package.json'], currentVersions: { 'package.json': '2.0.30' }, changes: [], classifications: [],
  aheadCount: 0, unpushedChanges: [], blockingIssues: [], canRelease: true, remoteChecked: false, statusFingerprint: 'fixture', profile }
const baseRun = { id: 'dismiss-run', appId: 'dismiss-ui', repoRoot: 'C:/fixture', branch: 'master', remoteName: 'origin', targetVersion: '2.0.31', tagName: 'v2.0.31',
  createTag: true, pushRemote: true, selectedTargets: [], status: 'failed', stage: 'pushing_branch', commitSha: 'b'.repeat(40),
  errorCode: 'push_branch_failed', errorMessage: '验收：上传失败', createdAt: '2026-10-12T07:00:00Z' }
let run = { ...baseRun }, server, browser, page
const errors = [], blocked = [], mutations = []
const report = { passed: false, boundary: 'Actual Vue dialog; mock APIs. Test-only exposed refs exercise transient checking/submitting/retrying states. No real build, push, cancellation or upload.', checks: [] }
try {
  const expose = { name: 'dismiss-fixture-state', enforce: 'pre', transform(code, id) {
    if (id.replaceAll('\\', '/').endsWith('/src/components/ReleaseModal.vue')) {
      return code.replace('</script>', 'defineExpose({testState:{checkingCandidate,publishing,autoSubmitting,retrying}})\n</script>')
    }
  } }
  server = await createServer({ configFile: false, root: fixture, plugins: [expose, vue()], resolve: { alias: { '@': path.join(root, 'src') } },
    optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: 0, fs: { allow: [root] } } })
  await server.listen()
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`
  const { chromium } = createRequire(import.meta.url)(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
  browser = await chromium.launch({ channel: process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge', headless: true })
  page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, locale: 'zh-CN' }); page.setDefaultTimeout(12000)
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin === origin) return route.continue()
    if (url.origin !== 'http://dismiss.fixture.invalid') { blocked.push(url.href); return route.abort() }
    const send = json => route.fulfill({ json, headers: { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' } })
    if (request.method() === 'OPTIONS') return send({})
    if (request.method() !== 'GET') mutations.push(url.pathname)
    if (url.pathname.endsWith('/release/preflight')) return send(preflight)
    if (url.pathname.endsWith('/release-config')) return send(config)
    if (url.pathname.endsWith('/release-profile')) return send(profile)
    if (url.pathname.endsWith('/release/notes-draft')) return send({ text: '验收说明', baseTag: 'v2.0.30', sourceFingerprint: 'fixture' })
    if (url.pathname === '/api/apps/dismiss-ui/releases') return send([run])
    if (url.pathname === '/api/releases/dismiss-run') return send({ run, targets: [], artifacts: [], deliveries: [], logs: [], retryConfirmationRequired: false })
    blocked.push(request.method() + ' ' + url.pathname); return route.abort()
  })
  const overlay = page.locator('.overlay').first()
  const close = page.locator('.m-head .icon')
  async function openRun(status) {
    run = { ...baseRun, status, stage: ['queued', 'running'].includes(status) ? 'building_targets' : status === 'failed' ? 'pushing_branch' : 'completed' }
    await page.evaluate(() => window.reopenFixture())
    if (!['queued', 'running'].includes(status)) {
      await page.getByRole('button', { name: '发布历史', exact: false }).click()
      await page.getByRole('button', { name: '查看 v2.0.31 的发布记录' }).click()
    }
    await page.locator('.release-progress-overview').waitFor()
  }
  async function staysOpen() {
    assert.equal(await close.isDisabled(), true)
    await overlay.click({ position: { x: 5, y: 5 } })
    assert.equal(await overlay.count(), 1)
  }
  async function closesAfterFinish() {
    await close.waitFor()
    assert.equal(await close.isEnabled(), true)
    await page.locator('.m-head h2').click()
    assert.equal(await overlay.count(), 1, 'inside click dismissed dialog')
    await overlay.click({ position: { x: 5, y: 5 } })
    await overlay.waitFor({ state: 'detached' })
  }
  await page.goto(origin)
  for (const status of ['queued', 'running']) {
    await openRun(status); await staysOpen(); report.checks.push(`${status}: backdrop ignored and close disabled`)
  }
  for (const phase of ['checkingCandidate', 'autoSubmitting', 'publishing', 'retrying']) {
    await openRun('failed'); await page.evaluate(phase => window.phaseFixture(phase), phase)
    await staysOpen(); await page.evaluate(() => window.phaseFixture(''))
    await closesAfterFinish(); report.checks.push(`${phase}: locked until transition completes`)
  }
  for (const status of ['failed', 'succeeded', 'cancelled']) {
    await openRun(status); await closesAfterFinish(); report.checks.push(`${status}: backdrop dismisses without cancellation`)
  }
  assert.deepEqual(errors, []); assert.deepEqual(blocked, [])
  assert.ok(mutations.every(path => path.endsWith('/release-profile') || path.endsWith('/release/notes-draft') || path.endsWith('/release/preflight')), 'dismissal sent a publishing or cancellation request')
  await openRun('running'); await page.screenshot({ path: path.join(evidence, 'running-locked.png') })
  report.passed = true
} finally {
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify({ ...report, errors, blocked, mutations }, null, 2))
  await browser?.close(); await server?.close()
}
console.log(JSON.stringify(report, null, 2))
