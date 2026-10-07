// Current Vue component + real browser with isolated API fixtures. No real project writes.
// Run with RUNDOCK_PLAYWRIGHT_MODULE pointing to Playwright when it is not installed locally.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const evidence = path.join(root, 'outputs/acceptance/release-settings')
const fixtureRoot = path.join(evidence, 'fixture')
mkdirSync(fixtureRoot, { recursive: true })
const checks = []
const modalSource = readFileSync(path.join(root, 'src/components/ReleaseModal.vue'), 'utf8')
const overviewTag = modalSource.match(/<ReleaseSetupOverview\b[^>]*>/)?.[0]
assert.ok(overviewTag?.includes('@saved="onConfigFileSaved"'), 'Saved configuration must reach the release model')
assert.ok(overviewTag?.includes('@advanced="openAdvancedSettings"'), 'Overview must open the parent advanced settings')
assert.ok(overviewTag?.includes('@busy="configScanning = $event"'), 'Busy state must reach the parent release controller')
const advancedTag = modalSource.match(/<details\b[^>]*ref="advancedSettings"[^>]*>/)?.[0]
assert.ok(advancedTag && !/\s(?::|v-bind:)?open(?:\s|=|>)/.test(advancedTag), 'Advanced settings must be collapsed initially')
const advancedFunction = modalSource.match(/async function openAdvancedSettings\(\) \{[\s\S]*?\n\}/)?.[0]
assert.ok(advancedFunction?.includes('advancedSettings.value.open = true'), 'Parent handler must open the details element')

// Use the actual parent handler with the real component, without mounting unrelated release controllers.
writeFileSync(path.join(fixtureRoot, 'Fixture.vue'), `<script setup>
import { nextTick, ref } from 'vue'
import ReleaseSetupOverview from '@/components/ReleaseSetupOverview.vue'
const config = ref(window.__SETUP_CONFIG__)
const advancedSettings = ref(null)
const savedCount = ref(0)
const configScanning = ref(false)
const busyEvents = ref([])
function onSaved(value) { config.value = value; savedCount.value += 1 }
function onBusy(value) { configScanning.value = value; busyEvents.value.push(value) }
${advancedFunction}
</script>
<template>
  <main class="fixture-shell">
    <h1>发布设置验收</h1>
    <ReleaseSetupOverview app-id="fixture" :config="config" :disabled="configScanning" :available="true" @saved="onSaved" @advanced="openAdvancedSettings" @busy="onBusy" />
    <details ref="advancedSettings" class="settings-advanced">
      <summary tabindex="0">高级设置（通常不用改）</summary>
      <label>构建命令<input value="npm run build" /></label>
      <p>.launcher/release.yaml</p>
    </details>
    <output aria-label="Saved event count">{{ savedCount }}</output>
    <output aria-label="Busy events">{{ JSON.stringify(busyEvents) }}</output>
  </main>
</template>
<style>.fixture-shell{width:min(900px,100%);padding:16px;margin:0 auto}h1{font-size:18px}.settings-advanced{margin-top:16px}output{display:none}</style>
`)
writeFileSync(path.join(fixtureRoot, 'entry.js'), "import { createApp } from 'vue'; import Fixture from './Fixture.vue'; import '@/styles.css'; createApp(Fixture).mount('#app')\n")
writeFileSync(path.join(fixtureRoot, 'index.html'), '<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Isolated release settings acceptance</title></head><body><div id="app"></div><script type="module" src="/entry.js"></script></body></html>')

const targetBase = { kind: 'desktop', versionGroup: 'desktop', workingDir: '.', enabled: true, detected: false, confidence: 100 }
const detected = {
  schemaVersion: 1, source: 'detected', confidence: 100, warnings: [],
  repoRoot: 'C:/fixture', configPath: '.launcher/release.yaml',
  versionGroups: [{ id: 'desktop', name: 'PC', currentVersion: '1.0.0', versionFiles: [] }],
  targets: [
    { ...targetBase, id: 'pc-cloud', name: 'PC · 云端', runner: { type: 'git-push', os: [] }, steps: { publish: 'tag-push' }, artifacts: [] },
    { ...targetBase, id: 'pc-local', name: 'PC · 本地', runner: { type: 'local', os: ['windows'] }, steps: { build: 'npm run build' }, artifacts: ['dist/**/*'] },
  ],
  automation: { provider: 'github-actions', workflow: 'release.yml', trigger: 'tag-push', releaseBranch: 'main', publishesRelease: true },
  fileRules: [], checkProfiles: [],
}
const existing = { ...detected, source: 'file' }
const apiOrigin = 'http://release-settings.fixture.invalid'
const server = await createServer({
  configFile: false, envFile: false, root: fixtureRoot,
  plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } },
  server: { host: '127.0.0.1', port: 0, strictPort: false, fs: { allow: [root] } },
  logLevel: 'error',
})
let browser
const runtimeErrors = []
const requests = []
try {
  await server.listen()
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  async function openFixture(initial, options = {}) {
    const page = await browser.newPage({ locale: 'zh-CN', viewport: { width: 1100, height: 780 } })
    page.on('pageerror', error => runtimeErrors.push(error.message))
    await page.addInitScript(({ initial, apiOrigin }) => {
      window.__SETUP_CONFIG__ = initial
      window.__LAUNCHER_BASE__ = apiOrigin
      localStorage.setItem('rundock.ui.locale', 'zh-CN')
    }, { initial, apiOrigin })
    await page.route('**/*', async route => {
      const request = route.request(), url = new URL(request.url())
      if (url.origin === origin) return route.continue()
      if (url.origin !== apiOrigin) {
        runtimeErrors.push(`Blocked unexpected network request: ${request.method()} ${url.origin}${url.pathname}`)
        return route.abort()
      }
      const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' }
      const send = (body, status = 200) => route.fulfill({ status, headers, contentType: 'application/json', body: JSON.stringify(body) })
      if (request.method() === 'OPTIONS') return send({})
      const body = request.postData() ? request.postDataJSON() : null
      requests.push({ method: request.method(), path: url.pathname, body })
      if (request.method() === 'POST' && url.pathname === '/api/apps/fixture/release-config/scan') {
        if (options.scanError) return send({ error: 'Fixture scan failed' }, 500)
        return send(detected)
      }
      if (request.method() === 'PUT' && url.pathname === '/api/apps/fixture/release-config/file') {
        if (options.saveConflict) return send({ error: '配置已由其他操作创建，请重新读取。', code: 'release_config_changed' }, 409)
        return send(existing)
      }
      runtimeErrors.push(`Unexpected API request ${request.method()} ${url.pathname}`)
      return send({ error: 'Unexpected fixture request' }, 400)
    })
    await page.goto(origin, { waitUntil: 'networkidle' })
    await page.getByRole('region', { name: '项目发布设置' }).waitFor()
    return page
  }

  const savedPage = await openFixture(existing)
  const overview = savedPage.getByRole('region', { name: '项目发布设置' })
  assert.equal(await overview.locator('.setup-row').count(), 1, 'Cloud and local definitions must share one PC row')
  assert.equal(await overview.locator('.setup-row > strong').innerText(), 'PC')
  assert.equal(await overview.getByText('已配置', { exact: true }).count(), 2)
  assert.equal(await overview.getByRole('button', { name: '保存并使用' }).count(), 0)
  assert.equal(await overview.getByRole('button', { name: '自动识别', exact: true }).count(), 0)
  assert.equal(await savedPage.getByRole('textbox').isVisible(), false, 'Technical fields must be hidden by default')
  assert.equal(await savedPage.getByText('.launcher/release.yaml', { exact: true }).isVisible(), false)
  await overview.getByRole('button', { name: '补充或调整设置' }).click()
  assert.equal(await savedPage.locator('.settings-advanced').evaluate(element => element.open), true)
  assert.equal(await savedPage.getByRole('textbox').isVisible(), true)
  assert.equal(await savedPage.locator('.settings-advanced summary').evaluate(element => element === document.activeElement), true)
  assert.equal(requests.length, 0, 'Reading existing configuration and opening details must not write or scan')
  checks.push('Existing PC cloud/local definitions merge; technical fields start hidden; existing files have no scan/save overwrite action')
  checks.push('Actual parent advanced handler opens the details and focuses its summary; source confirms saved event and collapsed parent integration')
  await savedPage.locator('.settings-advanced summary').click()
  await savedPage.screenshot({ path: path.join(evidence, 'existing-desktop.png'), fullPage: true })
  await savedPage.setViewportSize({ width: 360, height: 800 })
  assert.equal(await savedPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'No narrow-screen horizontal overflow')
  assert.equal(await overview.locator('.build-methods').evaluate(element => getComputedStyle(element).gridTemplateColumns.split(' ').length), 1)
  await savedPage.screenshot({ path: path.join(evidence, 'existing-mobile.png'), fullPage: true })
  checks.push('360px viewport has one-column build methods and no horizontal overflow')

  const newPage = await openFixture(null)
  await newPage.getByRole('button', { name: '自动识别', exact: true }).click()
  await newPage.locator('.setup-row').waitFor()
  assert.equal(await newPage.locator('.setup-row').count(), 1)
  await newPage.getByRole('button', { name: '保存并使用', exact: true }).click()
  await newPage.getByRole('status').filter({ hasText: '已保存。可到“发布”选择构建方式和发布端。' }).waitFor()
  assert.equal(await newPage.getByLabel('Saved event count').innerText(), '1')
  assert.equal(await newPage.getByLabel('Busy events').innerText(), '[true,false,true,false]')
  await newPage.getByText('已读取项目配置', { exact: true }).waitFor()
  assert.equal(await newPage.getByRole('button', { name: '保存并使用' }).count(), 0)
  const createRequest = requests.find(request => request.method === 'PUT')
  assert.equal(createRequest?.body.revision, 'missing', 'First setup must use a missing-file revision, not overwrite')
  const serialized = JSON.parse(createRequest.body.content)
  assert.deepEqual(serialized.targets, detected.targets)
  assert.deepEqual(serialized.versionGroups, detected.versionGroups)
  assert.deepEqual(serialized.automation, detected.automation)
  assert.ok(!('repoRoot' in serialized) && !('source' in serialized) && !('configPath' in serialized), 'Readonly source metadata must not be saved')
  checks.push('No-file setup scans and saves via PUT /release-config/file with revision missing, emits saved config, propagates/unlocks busy state, and hides creation actions after success')
  await newPage.screenshot({ path: path.join(evidence, 'saved-detected.png'), fullPage: true })

  const conflictPage = await openFixture(detected, { saveConflict: true })
  await conflictPage.getByRole('button', { name: '保存并使用' }).click()
  await conflictPage.getByRole('alert').filter({ hasText: '配置已由其他操作创建，请重新读取。' }).waitFor()
  assert.equal(await conflictPage.getByLabel('Saved event count').innerText(), '0')
  assert.equal(await conflictPage.getByLabel('Busy events').innerText(), '[true,false]')
  assert.equal(await conflictPage.getByRole('status').count(), 0)
  assert.equal(await conflictPage.getByText('已读取项目配置', { exact: true }).count(), 0)
  assert.equal(await conflictPage.getByRole('button', { name: '保存并使用' }).isEnabled(), true)
  await conflictPage.screenshot({ path: path.join(evidence, 'conflict-visible.png'), fullPage: true })
  checks.push('Concurrent-file 409 stays visible, emits no saved event, shows no success notice, and remains retryable')

  const scanErrorPage = await openFixture(null, { scanError: true })
  await scanErrorPage.getByRole('button', { name: '自动识别', exact: true }).click()
  await scanErrorPage.getByRole('alert').filter({ hasText: 'Fixture scan failed' }).waitFor()
  assert.equal(await scanErrorPage.getByRole('button', { name: '保存并使用' }).count(), 0)
  assert.equal(await scanErrorPage.getByRole('button', { name: '自动识别', exact: true }).isEnabled(), true)
  checks.push('Scan failure shows a retryable error and does not expose a false save result')
  assert.deepEqual(runtimeErrors, [])
  const report = {
    passed: true,
    boundary: 'Real current ReleaseSetupOverview Vue component and parent openAdvancedSettings handler in isolated Vite fixture; parent event bindings checked in source. Simulated HTTP API only; full ReleaseModal controllers, real filesystem persistence and release execution are not exercised.',
    checks,
    apiRequests: requests.map(({ method, path, body }) => ({ method, path, revision: body?.revision })),
  }
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify(report, null, 2))
} catch (error) {
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify({ passed: false, checks, error: String(error), runtimeErrors }, null, 2))
  throw error
} finally {
  await browser?.close()
  await server.close()
}
