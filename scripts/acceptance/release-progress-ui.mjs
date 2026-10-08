// Isolated real Vue components; no real app, build, GitHub or server APIs.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const evidence = path.join(root, 'outputs/release-progress')
const fixture = path.join(root, '.tmp/release-progress-ui')
await mkdir(evidence, { recursive: true }); await mkdir(fixture, { recursive: true })
await writeFile(path.join(fixture, 'index.html'), '<html lang="zh"><div id="app"></div><script type="module" src="/main.ts"></script></html>')
const source = '/@fs/' + root.replaceAll('\\', '/') + '/src'
await writeFile(path.join(fixture, 'main.ts'), `
import { createApp, h, reactive } from 'vue';
import Overview from '${source}/components/ReleaseProgressOverview.vue';
import Delivery from '${source}/components/ReleaseDeliveryStatus.vue';
import Cancel from '${source}/components/ReleaseCancelButton.vue';
import Artifacts from '${source}/components/LocalBuildArtifacts.vue';
import { setLocale } from '${source}/i18n/index.ts';
import '${source}/styles.css'; import '${source}/components/release/release-modal.css';
setLocale('zh-CN'); window.setTestLocale=setLocale; window.__LAUNCHER_BASE__='http://127.0.0.1:19998'; window.cancelRefreshes=0;
const targets=[{targetId:'android-local',build:true,package:false,publish:true,deploy:false,status:'waiting',stage:'waiting_publish',buildDone:true}];
const data=reactive({run:{id:'fixture',selectedTargets:targets,status:'running',stage:'delivery_publish',pushRemote:true,createTag:true,tagName:'android/v1.2.38',errorCode:'',errorMessage:'',versions:[{versionGroupId:'android',versionGroupName:'Android',tagName:'android/v1.2.38'}]},targets,deliveries:[{groupId:'android',state:'uploading',syncState:'unconfigured',syncMessage:'',manifestSha256:'fixture',errorMessage:''}],artifacts:[{id:'apk',targetId:'android-local',path:'release/LocalPlay-1.2.38.apk',name:'LocalPlay-1.2.38.apk',sizeBytes:88430720,sha256:'fixture',available:true},{id:'sig',targetId:'android-local',path:'release/signature.sig',name:'signature.sig',sizeBytes:428,sha256:'fixture',available:true}],definitions:[{id:'android-local',name:'Android',versionGroup:'android',delivery:{provider:'github'}}],localOnly:false,cloudHandoff:false,cloudBuild:null});
window.progressFixture=data;
createApp({setup:()=>()=>h('div',{class:'overlay'},[h('div',{class:'modal'},[
h('header',{class:'m-head'},[h('h2','发布 LocalPlay · 隔离验收')]),
h(Overview,data,{actions:()=>!data.localOnly && h(Cancel,{key:data.run.id,runId:data.run.id,status:data.run.status,onRefresh:()=>window.cancelRefreshes++})}),
h('div',{class:'m-body'},[
!data.localOnly && h(Delivery,data),
h(Artifacts,{runId:'fixture',artifacts:data.artifacts,outputDirectory:''}),
h('details',{open:true},[h('summary','执行日志'),h('pre',{style:'min-height:1000px'},'模拟日志\\n'.repeat(80))])
])])])}).mount('#app');
`)
const server = await createServer({ configFile: false, root: fixture, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } }, optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: 19483, strictPort: false, fs: { allow: [root] } } })
let browser
const report = { boundary: 'isolated production components, simulated states; no release or deployment', passed: false, checks: [] }
try {
  await server.listen()
  const port = server.httpServer.address().port
  browser = await chromium.launch({ channel: process.env.RUNDOCK_BROWSER_CHANNEL || 'msedge', headless: true })
  const page = await browser.newPage({ viewport: { width: 1280, height: 950 } })
  const errors = [], blocked = []
  let cancelCalls = 0, cancelMode = 'error', pendingCancel
  page.on('pageerror', error => errors.push(error.message))
  await page.route('**/*', async route => {
    const url = new URL(route.request().url())
    if (url.origin === 'http://127.0.0.1:19998' && url.pathname === '/api/releases/fixture/cancel' && route.request().method() === 'POST') {
      cancelCalls++
      if (cancelMode === 'hold') { pendingCancel = route; return }
      return route.fulfill({status: 500, contentType: 'application/json', body: JSON.stringify({error: {message: '模拟取消失败'}})})
    }
    if (url.hostname === '127.0.0.1' && url.port === String(port) && !url.pathname.startsWith('/api/')) return route.continue()
    blocked.push(url.pathname); await route.abort()
  })
  await page.goto(`http://127.0.0.1:${port}/`)
  const overview = page.locator('.release-progress-overview')
  await overview.getByText('正在上传 GitHub Release', { exact: true }).waitFor()
  assert.match(await overview.innerText(), /接下来：正式发布 Release/)
  assert.match(await overview.innerText(), /84.3 MB/)
  assert.equal(await overview.locator('progress').getAttribute('value'), null)
  assert.match(await page.locator('.delivery-files').innerText(), /LocalPlay-1.2.38.apk\s+84.3 MB/)
  assert.match(await page.locator('.artifact-list').innerText(), /428 B/)
  const cancel = overview.getByRole('button', {name:'取消执行', exact:true})
  assert.equal(await page.getByRole('button', {name:'取消执行', exact:true}).count(), 1)
  assert.ok((await cancel.boundingBox()).height >= 44)
  const waitingColor = await overview.locator('li.waiting').first().evaluate(el => getComputedStyle(el).color)
  const activeColor = await overview.locator('li.running').evaluate(el => getComputedStyle(el).color)
  assert.notEqual(waitingColor, activeColor)
  const contrast = await overview.locator('li.waiting').first().evaluate(el => {
    const luminance = rgb => {
      const values = rgb.match(/[\d.]+/g).slice(0,3).map(Number).map(v => v/255).map(v => v <= .04045 ? v/12.92 : ((v+.055)/1.055)**2.4)
      return values[0]*.2126 + values[1]*.7152 + values[2]*.0722
    }
    return (luminance(getComputedStyle(el).color)+.05)/(luminance(getComputedStyle(el.closest('section')).backgroundColor)+.05)
  })
  assert.ok(contrast >= 4.5, 'pending text is quieter but still readable')
  report.checks.push('upload headline, next steps, indeterminate animation, grouped artifact names and real sizes')
  const before = await overview.boundingBox()
  await page.locator('.m-body').evaluate(el => { el.scrollTop = el.scrollHeight })
  assert.deepEqual(await overview.boundingBox(), before)
  assert.ok(await cancel.isVisible())
  await page.locator('.m-body').evaluate(el => { el.scrollTop = 0 })
  await page.screenshot({ path: path.join(evidence, 'uploading-desktop.png') })
  report.checks.push('overall status remains fixed above scrollable logs')
  await page.emulateMedia({ reducedMotion: 'reduce' })
  assert.equal(await overview.locator('progress').evaluate(el => getComputedStyle(el).animationName), 'none')
  report.checks.push('reduced-motion disables animation while preserving textual status')
  await page.setViewportSize({ width: 390, height: 844 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  assert.ok((await page.locator('.m-body').boundingBox()).height > 180)
  await page.screenshot({ path: path.join(evidence, 'uploading-mobile.png') })
  report.checks.push('mobile status and artifacts fit without page overflow')
  await page.setViewportSize({ width: 1280, height: 950 })
  await cancel.click()
  await overview.locator('.release-cancel [role="alert"]').waitFor()
  assert.equal(await page.evaluate(() => progressFixture.run.status), 'running')
  assert.ok(await cancel.isEnabled())
  cancelMode = 'hold'
  await cancel.click()
  await page.waitForFunction(() => document.querySelector('.release-cancel button')?.disabled)
  assert.equal(await overview.getByRole('button', {name:'正在取消…'}).count(), 1)
  assert.equal(cancelCalls, 2)
  await pendingCancel.fulfill({status:200,contentType:'application/json',body:JSON.stringify({})})
  await page.waitForFunction(() => window.cancelRefreshes === 1)
  assert.equal(await page.evaluate(() => progressFixture.run.status), 'running', 'only backend status can mark cancellation complete')
  report.checks.push('large fixed cancel control, pending state, request failure feedback, no optimistic terminal status')
  await page.evaluate(() => { progressFixture.run.status = 'failed'; progressFixture.run.errorMessage = '上传连接中断，请重试'; progressFixture.deliveries[0].state = 'failed'; progressFixture.deliveries[0].errorMessage = '上传连接中断，请重试' })
  await overview.getByText('步骤失败：上传并核验 GitHub 附件', { exact: true }).waitFor()
  assert.equal(await overview.locator('progress').count(), 0)
  assert.match(await overview.innerText(), /上传连接中断/)
  assert.equal(await overview.locator('.release-cancel').count(), 0)
  assert.equal(await overview.locator('.result-symbol').innerText(), '!')
  await page.waitForFunction(() => getComputedStyle(document.querySelector('.status-symbol')).color === 'rgb(255, 170, 170)', null, {timeout:3000})
  const failureBackground = await overview.evaluate(el => getComputedStyle(el).backgroundColor)
  await page.screenshot({ path: path.join(evidence, 'upload-failed.png') })
  await page.setViewportSize({width:390,height:844})
  assert.ok((await page.locator('.m-body').boundingBox()).height > 180)
  await page.screenshot({path:path.join(evidence,'upload-failed-mobile.png')})
  await page.setViewportSize({width:1280,height:950})
  report.checks.push('failure identifies the stage and readable cause, without a busy spinner')
  await page.evaluate(() => { progressFixture.run.status = 'succeeded'; progressFixture.run.stage = 'completed'; progressFixture.run.errorMessage = ''; progressFixture.deliveries[0].state = 'published'; progressFixture.deliveries[0].errorMessage = ''; progressFixture.deliveries[0].syncState = 'pending' })
  await overview.getByText('构建与发布已完成', { exact: true }).waitFor()
  assert.equal(await overview.locator('.result-symbol').innerText(), '✓')
  await page.waitForFunction(() => getComputedStyle(document.querySelector('.status-symbol')).color === 'rgb(124, 230, 185)', null, {timeout:3000})
  assert.notEqual(await overview.evaluate(el => getComputedStyle(el).backgroundColor), failureBackground)
  assert.match(await overview.innerText(), /服务器同步尚未确认/)
  await page.screenshot({ path: path.join(evidence, 'published-sync-pending.png') })
  report.checks.push('Release success stays separate from unconfirmed server synchronization')
  await page.evaluate(() => { progressFixture.run.status = 'running'; progressFixture.run.stage = 'target_build'; progressFixture.deliveries = []; progressFixture.artifacts = []; progressFixture.targets[0].status = 'running'; progressFixture.targets[0].stage = 'build'; progressFixture.definitions[0].name = 'Windows 安装包 · 本地' })
  await overview.getByText('正在构建', { exact:true }).waitFor()
  await page.screenshot({path:path.join(evidence,'building-desktop.png')})
  await page.setViewportSize({width:390,height:844})
  assert.ok((await page.locator('.m-body').boundingBox()).height > 180)
  await page.screenshot({path:path.join(evidence,'building-mobile.png')})
  await page.setViewportSize({width:1280,height:950})
  await page.evaluate(() => { progressFixture.run.status = 'cancelled'; progressFixture.run.errorCode = 'cancelled' })
  await overview.locator('.result-symbol').filter({hasText:'−'}).waitFor()
  assert.equal(await overview.locator('.release-cancel').count(), 0)
  assert.equal(await overview.locator('progress').count(), 0)
  await page.evaluate(() => { progressFixture.run.errorCode = '' })
  await page.evaluate(() => { progressFixture.localOnly = true; progressFixture.definitions = []; progressFixture.deliveries = []; progressFixture.run.pushRemote = false; progressFixture.run.status = 'running'; progressFixture.run.stage = 'local_build' })
  await page.waitForFunction(() => !document.querySelector('.release-progress-overview').innerText.includes('GitHub'))
  assert.ok(!/GitHub|Release|推送/.test(await overview.innerText()))
  report.checks.push('local-only build does not promise GitHub upload')
  await page.evaluate(() => { progressFixture.localOnly = false; progressFixture.run.pushRemote = true; progressFixture.run.status = 'succeeded'; progressFixture.run.stage = 'completed'; progressFixture.cloudHandoff = true; progressFixture.cloudBuild = { state: 'pending' } })
  await overview.getByText('等待云端结果', { exact: true }).waitFor()
  assert.equal(await overview.locator('progress').count(), 1)
  await page.evaluate(() => window.setTestLocale('en'))
  await overview.getByText('Waiting for cloud results', { exact: true }).waitFor()
  assert.ok(!/[\u3400-\u9fff]/.test(await overview.innerText()))
  report.checks.push('cloud handoff remains in progress; status labels localize to English')
  assert.deepEqual(errors, [])
  assert.deepEqual(blocked, [], 'no real API requests should be attempted')
  report.passed = true
} finally {
  await writeFile(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  await browser?.close(); await server.close()
}
console.log(JSON.stringify(report, null, 2))
