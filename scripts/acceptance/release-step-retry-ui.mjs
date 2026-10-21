// Real release modal/model and retry API wiring, with isolated HTTP responses only.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const fixture = path.join(root, '.tmp', `release-step-retry-${Date.now()}`)
const evidence = path.join(root, 'outputs/acceptance/release-step-retry')
mkdirSync(fixture, { recursive: true }); mkdirSync(evidence, { recursive: true })
const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
writeFileSync(path.join(fixture, 'index.html'), '<html lang="zh"><meta charset="utf-8"><div id="app"></div><script type="module" src="/main.ts"></script></html>')
writeFileSync(path.join(fixture, 'main.ts'), `
import {createApp,h,ref,nextTick} from 'vue';import Modal from '${source}/components/ReleaseModal.vue';
import {setLocale} from '${source}/i18n/index.ts';import '${source}/styles.css';
window.__LAUNCHER_BASE__='http://retry.fixture.invalid';setLocale('zh-CN');window.setTestLocale=setLocale;
createApp({setup(){const visible=ref(true);window.reopenFixture=async()=>{visible.value=false;await nextTick();visible.value=true};
return()=>visible.value?h(Modal,{app:{id:'retry-ui',name:'launcher-platform',status:'stopped'},onClose:()=>visible.value=false}):null}}).mount('#app');
`)
const config = { ...JSON.parse(readFileSync(path.join(root, '.launcher/release.yaml'), 'utf8')), source: 'file', confidence: 1, warnings: [] }
const profile = { appId: 'retry-ui', buildMode: 'local', syncPolicy: 'auto', remoteName: 'origin', versionStrategy: 'tauri', preReleaseCommand: '', createTag: true, versionMode: 'auto' }
const preflight = { repoRoot: 'C:/fixture', branch: 'master', headSha: 'a'.repeat(40), remoteName: 'origin', remoteUrl: 'https://github.com/fixture/project',
  remotes: ['origin'], latestTag: 'v2.0.30', latestGroupTags: {}, commitsSinceTags: {}, suggestedVersion: '2.0.31', suggestedVersions: { product: '2.0.31' },
  versionStrategy: 'tauri', versionFiles: ['package.json'], currentVersions: { 'package.json': '2.0.30' }, changes: [], classifications: [],
  aheadCount: 0, unpushedChanges: [], blockingIssues: [], canRelease: true, remoteChecked: false, statusFingerprint: 'fixture', profile }
const selection = { targetId: 'windows', build: true, package: true, publish: true, deploy: false }
const baseRun = { id: 'retry-fixture', appId: 'retry-ui', repoRoot: 'C:/fixture', branch: 'master', remoteName: 'origin', targetVersion: '2.0.31', tagName: 'v2.0.31',
  createTag: true, pushRemote: true, selectedTargets: [selection], status: 'failed', stage: 'pushing_branch', commitSha: 'b'.repeat(40),
  errorCode: 'push_failed', errorMessage: '连接上传服务器失败。本地提交和版本记录已保留。', createdAt: '2026-10-10T00:00:00Z' }
const artifacts = [{ targetId: 'windows', path: '.tmp/release-assets/RunDock_2.0.31_x64-setup.exe', sizeBytes: 13107200, sha256: 'c'.repeat(64) },
  { targetId: 'windows', path: '.tmp/release-assets/RunDock_2.0.31_x64_en-US.msi', sizeBytes: 15833497, sha256: 'd'.repeat(64) }]
let run = { ...baseRun }, deliveryState = 'sealed', holdMetadata = true, pendingMetadata, pendingRetry, retryMode = 'hold', confirmationRequired = false
let targetOverrides, deliveryOverrides
const calls = [], blocked = [], errors = []
const view = () => ({ run, artifacts, logs: [], retryConfirmationRequired: confirmationRequired,
  targets: targetOverrides || [{ ...selection, name: 'Windows 安装包', versionGroup: 'product', status: 'waiting', stage: 'waiting_publish', buildDone: true, packageDone: true }],
  deliveries: deliveryOverrides || [{ runId: run.id, groupId: 'product', state: deliveryState, manifestSha256: 'e'.repeat(64), syncState: 'unconfigured', syncMessage: '', errorCode: '', errorMessage: '' }] })
const report = { passed: false, boundary: 'actual Vue release dialog and existing retry handler; all API responses simulated, no Git push, build or GitHub upload', checks: [], retryRequests: [] }
let server, browser, context, page
async function until(check) {
  const end = Date.now() + 10000
  while (Date.now() < end) { if (await check()) return; await new Promise(resolve => setTimeout(resolve, 50)) }
  throw new Error('Timed out waiting for fixture request')
}
try {
  const listener = net.createServer()
  await new Promise((resolve, reject) => { listener.once('error', reject); listener.listen(0, '127.0.0.1', resolve) })
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve))
  server = await createServer({ configFile: false, root: fixture, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } },
    optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port, strictPort: true, fs: { allow: [root] } } })
  await server.listen()
  const { chromium } = createRequire(import.meta.url)(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
  browser = await chromium.launch({ channel: process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge', headless: true })
  context = await browser.newContext({ viewport: { width: 1440, height: 1050 }, locale: 'zh-CN' })
  await context.tracing.start({ screenshots: true, snapshots: true })
  page = await context.newPage(); page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin === `http://127.0.0.1:${port}`) return route.continue()
    if (url.origin !== 'http://retry.fixture.invalid') { blocked.push(url.href); return route.abort() }
    const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' }
    const send = (json, status = 200) => route.fulfill({ status, json, headers })
    if (request.method() === 'OPTIONS') return send({})
    calls.push({ path: url.pathname, method: request.method() })
    if (url.pathname.endsWith('/release/preflight')) return send(preflight)
    if (url.pathname.endsWith('/release-config')) return send(config)
    if (url.pathname.endsWith('/release-profile')) return send(profile)
    if (url.pathname.endsWith('/release/notes-draft')) return send({ text: '验收说明', baseTag: 'v2.0.30', sourceFingerprint: 'fixture' })
    if (url.pathname.endsWith('/release/preparations')) return send([])
    if (url.pathname === '/api/apps/retry-ui/releases') return send([run])
    if (url.pathname === '/api/releases/retry-fixture') {
      if (holdMetadata) { pendingMetadata = route; return }
      return send(view())
    }
    if (url.pathname.endsWith('/retry')) {
      report.retryRequests.push({ path: url.pathname, body: request.postDataJSON() })
      if (retryMode === 'hold') { pendingRetry = route; return }
      run = { ...run, status: 'queued', errorCode: '', errorMessage: '' }
      return send(run)
    }
    blocked.push(request.method() + ' ' + url.pathname); return route.abort()
  })
  await page.goto(`http://127.0.0.1:${port}`)
  const overview = page.locator('.release-progress-overview'), retry = page.locator('.run-actions .retry-submit')
  async function openHistory() {
    await page.locator('.history-button').click(); await page.locator('.history-row').click()
    await overview.waitFor()
  }
  async function scenario(changes = {}, state = 'sealed', needsConfirmation = false) {
    holdMetadata = false; confirmationRequired = needsConfirmation; run = { ...baseRun, ...changes }; deliveryState = state
    await page.evaluate(() => window.reopenFixture())
    if (['queued', 'running'].includes(run.status)) await overview.waitFor()
    else await openHistory()
    await page.waitForFunction(() => document.querySelector('.retry-submit')?.disabled === false || !document.querySelector('.retry-submit'))
  }
  await openHistory(); await until(() => pendingMetadata)
  assert.equal(await retry.count(), 1); assert.equal(await retry.isDisabled(), true)
  assert.equal(report.retryRequests.length, 0)
  holdMetadata = false; await pendingMetadata.fulfill({ json: view(), headers: { 'access-control-allow-origin': '*' } })
  await page.waitForFunction(() => document.querySelector('.retry-submit')?.disabled === false)
  assert.equal(await overview.locator('.step-retry').count(), 0, 'one primary action, no duplicate step button')
  assert.equal(await page.locator('.m-body .retry-submit').count(), 0)
  const buttonBox = await retry.boundingBox()
  assert.ok(buttonBox.height >= 44 && buttonBox.y + buttonBox.height < 1050)
  await page.screenshot({ path: path.join(evidence, 'push-failed-desktop.png') })
  report.checks.push('single 44px retry is visible in the fixed footer and disabled until metadata arrives')
  await retry.focus(); await page.keyboard.press('Enter'); await until(() => pendingRetry)
  assert.equal(await retry.isDisabled(), true); assert.equal(await retry.getAttribute('aria-busy'), 'true')
  assert.equal(await page.locator('.retry-submit').isDisabled(), true)
  await page.mouse.click(buttonBox.x + buttonBox.width / 2, buttonBox.y + buttonBox.height / 2, { clickCount: 2 })
  assert.equal(report.retryRequests.length, 1)
  await page.screenshot({ path: path.join(evidence, 'retrying-desktop.png') })
  await pendingRetry.fulfill({ status: 500, json: { error: '验收：上传连接仍不可用' }, headers: { 'access-control-allow-origin': '*' } })
  await page.getByText('验收：上传连接仍不可用', { exact: true }).waitFor()
  await page.waitForFunction(() => document.querySelector('.retry-submit')?.disabled === false)
  assert.equal(await page.locator('.run-actions [role="alert"]').count(), 1)
  report.checks.push('keyboard retry reaches the same run once; duplicate clicks blocked; request error remains visible beside retry')
  retryMode = 'accepted'; await retry.click()
  await page.waitForFunction(() => !document.querySelector('.retry-submit'))
  assert.equal(report.retryRequests.length, 2)
  assert.ok(report.retryRequests.every(call => call.path === '/api/releases/retry-fixture/retry' && call.body.externalActionsConfirmed === true))
  report.checks.push('accepted retry resumes the existing record and hides the retry action while work runs')
  await scenario({ stage: 'delivery_publish', errorCode: 'upload_unconfirmed' }, 'failed')
  assert.match(await overview.innerText(), /步骤失败：上传核对|步骤失败：上传并核验 GitHub 附件/)
  await page.setViewportSize({ width: 390, height: 844 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  const mobile = await retry.boundingBox()
  assert.ok(mobile.height >= 44 && mobile.y + mobile.height <= 844)
  assert.ok((await page.locator('.m-body').boundingBox()).height >= 150)
  await page.screenshot({ path: path.join(evidence, 'upload-failed-mobile.png') })
  await page.setViewportSize({ width: 1440, height: 1050 })
  await page.evaluate(() => window.setTestLocale('en'))
  await retry.getByText('Retry upload', { exact: true }).waitFor()
  assert.ok(!/[\u3400-\u9fff]/.test(await page.locator('.run-actions').innerText()))
  await page.screenshot({ path: path.join(evidence, 'upload-failed-english.png') })
  await page.evaluate(() => window.setTestLocale('zh-CN'))
  report.checks.push('attachment upload failure has the same localized action; mobile fits and English labels are complete')
  await scenario({ stage: 'delivery_publish' }, 'publishing')
  assert.match(await overview.innerText(), /正式发布/)
  report.checks.push('failed upload and Release confirmation share one fixed retry action')
  await scenario({ stage: 'delivery_preflight', errorCode: 'github_connection_interrupted', errorMessage: '读取 GitHub 自动发布配置失败：读取 GitHub 信息时连接中断，请稍后重试' })
  await overview.getByText('GitHub 连接失败', { exact: true }).waitFor()
  assert.match(await overview.innerText(), /不代表发布配置失效/)
  assert.match(await page.locator('.target-progress-row').innerText(), /生成成功[\s\S]*等待上传到? GitHub/)
  assert.equal(await page.locator('.m-body .alert.error').count(), 0)
  assert.equal(await page.locator('.execution-details[open]').count(), 0)
  assert.equal(await page.locator('.artifacts[open]').count(), 0)
  const fixedBefore = await retry.boundingBox()
  await page.locator('.execution-details').filter({ has: page.locator('.log-box') }).locator('summary').click()
  await page.locator('.log-box').evaluate(el => { el.style.minHeight = '1500px' })
  await page.locator('.m-body').evaluate(el => { el.scrollTop = el.scrollHeight })
  assert.deepEqual(await retry.boundingBox(), fixedBefore)
  await page.locator('.m-body').evaluate(el => { el.scrollTop = 0 })
  await page.locator('.execution-details').filter({ has: page.locator('.log-box') }).locator('summary').click()
  await page.screenshot({ path: path.join(evidence, 'connection-failed-desktop.png') })
  for (const viewport of [{ width: 390, height: 844 }, { width: 1280, height: 620 }]) {
    await page.setViewportSize(viewport)
    const box = await retry.boundingBox()
    assert.ok(box.y >= 0 && box.y + box.height <= viewport.height)
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    assert.ok((await page.locator('.m-body').boundingBox()).height >= 100)
    await page.screenshot({ path: path.join(evidence, `connection-failed-${viewport.width}.png`) })
  }
  await page.setViewportSize({ width: 1440, height: 1050 })
  await page.locator('.progress-flow summary').click()
  assert.match(await overview.locator('li.failed').innerText(), /连接 GitHub/)
  assert.match(await overview.locator('li.succeeded').filter({hasText:'核对保存'}).innerText(), /已完成/)
  report.checks.push('network interruption explained, build success separate from waiting upload, collapsed details; footer fixed on desktop, narrow and short screens')
  await scenario({}, 'sealed', true)
  const beforeConfirmation = report.retryRequests.length
  await retry.click(); await page.locator('.action-confirm-overlay').waitFor()
  assert.equal(report.retryRequests.length, beforeConfirmation)
  await page.locator('.action-confirm-overlay').getByRole('button', { name: '返回检查', exact: true }).click()
  assert.equal(await retry.isEnabled(), true)
  report.checks.push('backend-required custom-action confirmation remains mandatory before any retry request')
  targetOverrides = [
    { ...selection, targetId: 'android', name: 'Android', versionGroup: 'android', buildDone: true, packageDone: true, status: 'waiting', stage: 'waiting_publish' },
    { ...selection, targetId: 'pc', name: 'PC 客户端', versionGroup: 'pc', buildDone: false, packageDone: false, status: 'running', stage: 'build' },
    { ...selection, targetId: 'web', name: 'Web 服务', versionGroup: 'web', buildDone: false, packageDone: false, status: 'waiting', stage: 'waiting' },
  ]
  deliveryOverrides = []
  await scenario({ status: 'running', stage: 'target_build', errorCode: '', errorMessage: '', selectedTargets: targetOverrides })
  await page.locator('.target-progress-row').filter({hasText:'Android'}).waitFor()
  const android = page.locator('.target-progress-row').filter({hasText:'Android'})
  const pc = page.locator('.target-progress-row').filter({hasText:'PC 客户端'})
  const web = page.locator('.target-progress-row').filter({hasText:'Web 服务'})
  assert.match(await android.innerText(), /生成成功/)
  assert.match(await pc.innerText(), /生成中[\s\S]*等待生成完成/)
  assert.match(await web.innerText(), /等待生成[\s\S]*等待生成完成/)
  const cancel = overview.getByRole('button', {name:'取消执行', exact:true})
  assert.ok((await cancel.boundingBox()).height >= 44)
  await page.emulateMedia({reducedMotion:'reduce'})
  assert.equal(await overview.locator('progress').first().evaluate(el=>getComputedStyle(el).animationName), 'none')
  await page.screenshot({path:path.join(evidence,'mixed-build-states.png')})
  targetOverrides = undefined; deliveryOverrides = undefined
  await scenario({ status: 'running', stage: 'delivery_publish', errorCode: '', errorMessage: '' }, 'uploading')
  await page.locator('.target-progress-row .target-phase.running').waitFor()
  assert.equal(await page.locator('.target-progress-row .target-phase.succeeded').count(), 1)
  await page.screenshot({path:path.join(evidence,'upload-running.png')})
  await scenario({ status: 'succeeded', stage: 'completed', errorCode: '', errorMessage: '' }, 'published')
  await overview.locator('.result-symbol').filter({hasText:'✓'}).waitFor()
  assert.equal(await page.locator('.target-progress-row .target-phase.succeeded').count(), 2)
  assert.equal(await retry.count(), 0)
  await page.screenshot({path:path.join(evidence,'published.png')})
  report.checks.push('three platforms distinguish finished, active and pending generation; upload preserves build success; success, cancel entry and reduced motion verified')
  for (const localArtifactsState of ['cleaning', 'cleaned']) {
    deliveryOverrides = [{ ...view().deliveries[0], localArtifactsState, url: 'https://github.com/fixture/project/releases/tag/v2.0.31' }]
    await scenario({ status: 'succeeded', stage: 'completed', errorCode: '', errorMessage: '' }, 'published')
    assert.match(await page.locator('.storage-note').innerText(), localArtifactsState === 'cleaned' ? /本地副本已清理/ : /本地副本清理中/)
    assert.equal(await page.locator('.delivery-status a').getAttribute('href'), 'https://github.com/fixture/project/releases/tag/v2.0.31')
    assert.match(await page.locator('.completion-banner').innerText(), /旧本地副本按规则清理/)
    assert.match(await overview.locator('.artifact-total').innerText(), /原始大小/)
    assert.equal(await page.locator('.storage-note').evaluate(el => getComputedStyle(el).color), await page.locator('.delivery-status dt').first().evaluate(el => getComputedStyle(el).color))
    assert.equal(await retry.count(), 0)
  }
  await page.screenshot({path:path.join(evidence,'published-cleaned.png')})
  deliveryOverrides = undefined
  report.checks.push('cleaning and cleaned copies retain successful release status and GitHub download link; no misleading retry action')
  await scenario({ stage: 'delivery_preflight', errorCode: 'github_connection_interrupted' })
  const beforeDefer = report.retryRequests.length
  await page.locator('.run-actions').getByRole('button',{name:'稍后处理',exact:true}).click()
  await page.waitForFunction(()=>!document.querySelector('.modal'))
  assert.equal(report.retryRequests.length,beforeDefer)
  await page.evaluate(()=>window.reopenFixture()); await openHistory(); await retry.waitFor()
  assert.equal(report.retryRequests.length,beforeDefer)
  report.checks.push('defer closes without retrying; the same saved run can be reopened from history')
  for (const changes of [{ stage: 'target_build' }, { errorCode: 'build_changed_tree' }, { commitSha: '' }, { status: 'succeeded', stage: 'completed' }]) {
    await scenario(changes, changes.status === 'succeeded' ? 'published' : 'sealed')
    if (changes.stage === 'target_build') assert.match(await retry.innerText(), /重新生成|重试构建/)
    else assert.equal(await retry.count(), 0)
  }
  assert.deepEqual(errors, []); assert.deepEqual(blocked, [])
  assert.ok(calls.filter(call => call.method === 'POST').every(call => /\/(retry|preflight|notes-draft)$/.test(call.path)))
  report.checks.push('build retry remains distinct; changed source, missing commit and completed runs cannot retry; no build/create/GitHub calls')
  report.passed = true
} catch (error) {
  report.error = String(error)
  report.requests = calls
  report.browserErrors = errors
  report.blocked = blocked
  if (page) report.dom = await page.evaluate(() => ({ inlineDisabled: document.querySelector('.step-retry')?.disabled, footerDisabled: document.querySelector('.retry-submit')?.disabled, text: document.querySelector('.modal')?.innerText }))
  if (page) await page.screenshot({ path: path.join(evidence, 'failure.png') }).catch(() => {})
  throw error
} finally {
  if (context) await context.tracing.stop({ path: path.join(evidence, 'browser-trace.zip') }).catch(() => {})
  await browser?.close(); await server?.close()
  writeFileSync(path.join(evidence, 'result.json'), JSON.stringify(report, null, 2) + '\n')
  console.log(`${report.passed ? 'PASS' : 'FAIL'}: ${path.join(evidence, 'result.json')}`)
}
