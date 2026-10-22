// Isolated Vue/browser acceptance. All API calls are intercepted; no real restart.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { preview } from 'vite'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const evidence = path.join(root, 'outputs/acceptance/restart-ui')
mkdirSync(evidence, { recursive: true })
const server = await preview({ root, preview: { host: '127.0.0.1', port: 0 } })
let browser, page
const checks = []
try {
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  page = await browser.newPage({ locale: 'zh-CN', viewport: { width: 1200, height: 800 } })
  const pageErrors = []
  page.on('pageerror', error => pageErrors.push(error.message))
  let structured = null
  let kind = 'external', blocked = false, expired = false, fail = false, plans = 0, confirms = 0, managed = 0, newBackend = false, healthReads = 0
  let observed = true, runtimeState = 'running', appStatus = 'running', planError = false, runtimeReads = 0, portAllowed = false, portError = false, portFail = false, portPlans = 0, portConfirms = 0
  const runtimeMessages = {
    running: '项目在外部启动，当前仅监测；可点击重启核验进程归属，确认后重新启动。',
    unknown: '暂时无法确认项目状态，已暂停启动，请稍后重新检查。',
    reserved: '项目端口被 Windows 保留，关闭应用程序无法释放，请调整项目端口配置。',
    checking: '正在检查项目进程和端口…',
    conflict: '项目端口已被其他程序占用，可查看占用程序并处理。',
  }
  const fixture = () => ({ id: 'fixture', name: 'Restart fixture', entryScript: 'C:/fixture/start.cmd', cwd: 'C:/fixture', args: [], env: {}, tags: [], services: [], portHints: [], status: appStatus, adapterType: 'batch', pid: 501,
    runtimeCheck: observed ? { state: runtimeState, message: runtimeMessages[runtimeState], conflicts: [], reservedPorts: runtimeState === 'reserved' ? [4310] : [] } : undefined })
  await page.addInitScript(() => { window.__LAUNCHER_BASE__ = 'http://fixture.invalid'; localStorage.setItem('rundock.ui.locale', 'zh-CN') })
  await page.route('http://fixture.invalid/**', async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method()
    const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' }
    const send = (body, status = 200) => route.fulfill({ status, headers, contentType: 'application/json', body: JSON.stringify(body) })
    if (method === 'OPTIONS') return send({})
    if (method === 'GET') {
      if (url.pathname === '/api/health') { healthReads++; return send({ status: 'ok', apiVersion: '2', capabilities: 'release-v2', instanceId: newBackend ? 'new' : 'old' }) }
      if (url.pathname === '/api/groups' || url.pathname === '/api/cloud-builds') return send([])
      if (url.pathname === '/api/apps') return send([fixture()])
      if (url.pathname === '/api/apps/fixture') return send(fixture())
      if (url.pathname.endsWith('/startup-issue')) return send({ code: 'port_in_use', ports: [4310], conflicts: [{ port: 4310, pid: 502, name: 'node.exe', safe: false }], canRecover: false, reason: '没有关闭该程序的权限，请从原程序退出' })
      return send({})
    }
    if (url.pathname.endsWith('/restart-plan')) {
      plans++
      await new Promise(resolve => setTimeout(resolve, 180))
      if (planError) return send({ error: '当前后台尚未更新，请退出并用新版启动器打开 RunDock 一次，再使用重启。' }, 409)
      return send({ kind, canRestart: !blocked, message: blocked ? '有构建、发布或发布检查正在执行，请等待结束后再重启；未停止任何项目' : kind === 'self' ? '将由独立启动器重启 RunDock 后台，等待新实例连接。' : '已核实这些进程属于此项目。确认后关闭并重新启动；未保存内容可能丢失。', processes: blocked ? [] : [{ pid: 501, name: 'cmd.exe' }, { pid: 502, name: 'node.exe' }], confirmationToken: blocked ? undefined : `token-${plans}`, expiresAt: new Date(Date.now() + (expired ? -1000 : 60000)).toISOString(), ...structured })
    }
    if (url.pathname.endsWith('/restart-confirm')) {
      confirms++
      assert.match(request.postDataJSON().confirmationToken, /^token-/)
      if (fail) return send({ error: '进程身份已变化，未停止任何程序' }, 409)
      return send(kind === 'self' ? { restarting: true, instanceId: 'old' } : { restarted: true })
    }
    if (url.pathname.endsWith('/restart')) { managed++; return send({ restarted: true, app: fixture() }) }
    if (url.pathname.endsWith('/runtime-check')) { runtimeReads++; return send(fixture()) }
    if (url.pathname.endsWith('/port-resolution')) {
      portPlans++
      if (portError) return send({ error: '无法读取项目启动脚本' }, 409)
      return send({ state: 'conflict', message: '端口被其他程序占用，确认关闭后将自动启动项目', conflicts: [{ port: 4310, pid: 502, name: 'node.exe', canClose: portAllowed, reason: portAllowed ? '' : '没有关闭该程序的权限，请从原程序退出' }], reservedPorts: [], canResolve: portAllowed, confirmationToken: portAllowed ? `port-${portPlans}` : undefined, expiresAt: new Date(Date.now() + (expired ? -1000 : 60000)).toISOString() })
    }
    if (url.pathname.endsWith('/resolve-ports')) {
      portConfirms++
      assert.match(request.postDataJSON().confirmationToken, /^port-/)
      return portFail ? send({ error: '进程身份已变化，未停止任何程序' }, 409) : send({ started: true })
    }
    throw Error(`Unexpected write ${method} ${url.pathname}`)
  })
  const address = `http://127.0.0.1:${server.httpServer.address().port}`
  await page.goto(address, { waitUntil: 'domcontentloaded' })
  const card = page.locator('article.card')
  const restart = card.getByRole('button', { name: '重启', exact: true })
  const dialog = page.getByRole('dialog', { name: '重启 · Restart fixture' })
  const noGenericRetry = async scope => {
    assert.equal(await scope.getByRole('button', { name: /^(重试检测|刷新运行状态|重新检查|重新核验)$/ }).count(), 0)
  }
  await restart.waitFor()
  assert.equal(await card.getByRole('button', { name: '重新检查', exact: true }).count(), 0)
  await restart.click()
  await dialog.getByText('正在检查运行情况…').waitFor()
  await dialog.getByRole('button', { name: '确认重启', exact: true }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '取消', exact: true }).evaluate(el => el === document.activeElement), true)
  await page.keyboard.press('Escape')
  assert.equal(await dialog.isVisible(), false)
  assert.equal(confirms, 0)
  assert.equal(await restart.evaluate(el => el === document.activeElement), true)
  checks.push('Observed project keeps Restart; verification is read-only; native dialog cancel, Escape and focus restoration')
  blocked = true
  await restart.click()
  await dialog.getByText(/有构建、发布/).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '确认重启', exact: true }).count(), 0)
  await noGenericRetry(dialog)
  assert.deepEqual(await dialog.getByRole('button').allTextContents(), ['关闭'])
  await page.screenshot({ path: path.join(evidence, 'blocked-reason.png') })
  await dialog.getByRole('button', { name: '关闭' }).click()
  checks.push('Active build/check/release displays the blocker and Close, with no generic recheck or restart')
  structured = {reasonCode:'active_launcher', message:'服务仍由外部启动器运行，无法确认启动器是否还管理其他任务。', nextSteps:['请先在原终端或开发工具中停止项目，避免它自动拉起服务；随后点击“重新检查”。'], processes:[{pid:502,name:'node.exe',entry:'C:/fixture/preview.js'}]}
  await restart.click()
  await dialog.getByText('如何处理',{exact:true}).waitFor()
  await dialog.getByText('C:/fixture/preview.js',{exact:true}).waitFor()
  const confirmBeforeCheck=confirms
  blocked=false
  structured={mode:'detached_worker',message:'原启动器已退出，独立服务归属已核实。确认后结束下列进程，再按保存的配置启动并接管。',processes:[{pid:502,name:'node.exe',entry:'C:/fixture/preview.js'}],launchCommand:'npm.cmd run dev',launchDirectory:'C:/fixture',nextSteps:['请先保存工作；调试连接和未完成任务会中断。','启动命令可能与当前预览不同，请确认后再继续。']}
  await dialog.getByRole('button',{name:'重新检查',exact:true}).click()
  await dialog.getByRole('button',{name:'重启并接管',exact:true}).waitFor()
  assert.equal(confirms,confirmBeforeCheck)
  await dialog.getByText('npm.cmd run dev',{exact:true}).waitFor()
  await page.screenshot({path:path.join(evidence,'external-takeover.png')})
  await dialog.getByRole('button',{name:'重启并接管',exact:true}).click()
  await dialog.getByText('项目已重新启动，正在等待服务就绪。').waitFor()
  await dialog.getByRole('button',{name:'完成',exact:true}).click()
  structured=null
  checks.push('Blocked external launch shows matching PID and next steps; recheck never restarts; detached takeover shows the saved command and requires explicit confirmation')
  blocked = false; expired = true
  await restart.click()
  await dialog.getByText('方案已过期，请更新后重新确认。').waitFor()
  assert.equal(await dialog.getByRole('button', { name: '确认重启', exact: true }).count(), 0)
  const plansBeforeRenew = plans, confirmsBeforeRenew = confirms
  expired = false
  await dialog.getByRole('button', { name: '更新方案', exact: true }).click()
  await dialog.getByRole('button', { name: '确认重启', exact: true }).waitFor()
  assert.equal(plans, plansBeforeRenew + 1)
  assert.equal(confirms, confirmsBeforeRenew)
  await dialog.getByRole('button', { name: '取消' }).click()
  checks.push('Expired valid confirmation can be renewed, but renewal never executes a restart')
  planError = true
  await restart.click()
  await dialog.getByRole('alert').filter({ hasText: '请先退出软件，再用新版启动器打开' }).waitFor()
  assert.deepEqual(await dialog.getByRole('button').allTextContents(), ['关闭'])
  await dialog.getByRole('button', { name: '关闭' }).click()
  planError = false; fail = true
  await restart.click()
  await dialog.getByRole('button', { name: '确认重启', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: '进程身份已变化' }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '确认重启', exact: true }).count(), 0)
  assert.equal(await dialog.getByText(/已核实这些进程/).count(), 0)
  assert.deepEqual(await dialog.getByRole('button').allTextContents(), ['关闭'])
  await dialog.getByRole('button', { name: '关闭' }).click()
  fail = false
  await restart.click()
  await dialog.getByRole('button', { name: '确认重启', exact: true }).click()
  await dialog.getByText('项目已重新启动，正在等待服务就绪。').waitFor()
  await dialog.getByRole('button', { name: '完成' }).click()
  checks.push('Unsupported backend and changed identity show the error without stale success claims or fake repair buttons; reopening requires fresh confirmation')
  kind = 'self'
  await restart.click()
  await dialog.getByRole('button', { name: '重启软件', exact: true }).click()
  await dialog.getByText('软件正在重启，等待重新连接…').waitFor()
  const before = healthReads
  await page.waitForFunction(() => document.querySelector('dialog[open] .spinner') !== null)
  await page.keyboard.press('Escape')
  assert.equal(await dialog.isVisible(), true)
  assert.equal(await dialog.getByRole('button', { name: '取消' }).isDisabled(), true)
  await page.emulateMedia({ reducedMotion: 'reduce' })
  assert.equal(await dialog.locator('.spinner').evaluate(el => getComputedStyle(el).animationName), 'none')
  await page.screenshot({ path: path.join(evidence, 'self-reconnecting.png') })
  await new Promise(resolve => setTimeout(resolve, 1100))
  assert.ok(healthReads > before)
  assert.equal(await dialog.getByText('后台已重启，连接已恢复。').count(), 0)
  newBackend = true
  await dialog.getByText('后台已重启，连接已恢复。').waitFor()
  await dialog.getByRole('button', { name: '完成' }).click()
  checks.push('Self restart waits for a new backend instance, blocks duplicate submission and honors reduced motion')
  for (const state of ['unknown', 'reserved', 'checking']) {
    runtimeState = state; appStatus = state === 'reserved' ? 'failed' : state
    await page.reload()
    await card.locator('.runtime-notice').waitFor()
    await noGenericRetry(card)
    assert.equal(await card.getByRole('button', { name: /^(启动|重新启动|重启|关闭占用程序并启动)$/ }).count(), 0)
    await card.getByLabel('更多操作', { exact: true }).click()
    await noGenericRetry(card)
    if (state === 'unknown') {
      await card.getByText('暂时读不到项目状态，尚未启动。状态会自动更新。').waitFor()
      await page.screenshot({ path: path.join(evidence, 'status-only.png') })
    }
    if (state === 'reserved') await card.getByText('这个端口由系统保留，请在项目中换一个端口。', { exact: true }).waitFor()
  }
  assert.equal(runtimeReads, 0)
  checks.push('Unknown, reserved and checking states show information without fake retry/start buttons, including More; rendering sends no manual check')
  runtimeState = 'conflict'; appStatus = 'failed'
  await page.reload()
  const portDialog = page.getByRole('dialog', { name: '处理占用', exact: true })
  const portAction = card.getByRole('button', { name: '处理占用', exact: true })
  await portAction.click()
  await portDialog.getByText('无法关闭此程序，请在该程序中退出。', { exact: true }).waitFor()
  assert.deepEqual(await portDialog.getByRole('button').allTextContents(), ['关闭'])
  assert.equal(await portDialog.getByText('端口被占用。确认关闭占用程序后，会自动启动此项目。').count(), 0)
  await page.screenshot({ path: path.join(evidence, 'port-permission-reason.png') })
  await portDialog.getByRole('button', { name: '关闭', exact: true }).click()
  portError = true
  await portAction.click()
  await portDialog.getByRole('alert').filter({ hasText: '无法读取项目启动脚本' }).waitFor()
  assert.deepEqual(await portDialog.getByRole('button').allTextContents(), ['关闭', '重新查看'])
  portError = false
  const beforePortRefresh = portConfirms
  await portDialog.getByRole('button', { name: '重新查看', exact: true }).click()
  await portDialog.getByText('无法关闭此程序，请在该程序中退出。', { exact: true }).waitFor()
  assert.equal(portConfirms, beforePortRefresh)
  await portDialog.getByRole('button', { name: '关闭', exact: true }).click()
  checks.push('Unsafe port owner and unreadable entry show specific reasons; viewing a fresh plan never closes a process')
  portError = false; portAllowed = true; expired = true
  await portAction.click()
  await portDialog.getByRole('button', { name: '更新方案', exact: true }).waitFor()
  assert.equal(await portDialog.getByRole('button', { name: '关闭并启动', exact: true }).count(), 0)
  expired = false
  await portDialog.getByRole('button', { name: '更新方案', exact: true }).click()
  await portDialog.getByRole('button', { name: '关闭并启动', exact: true }).waitFor()
  assert.equal(portConfirms, 0)
  portFail = true
  await portDialog.getByRole('button', { name: '关闭并启动', exact: true }).click()
  await portDialog.getByRole('alert').filter({ hasText: '进程身份已变化' }).waitFor()
  assert.deepEqual(await portDialog.getByRole('button').allTextContents(), ['关闭', '重新查看'])
  assert.equal(await portDialog.getByText('端口被占用。确认关闭占用程序后，会自动启动此项目。').count(), 0)
  await portDialog.getByRole('button', { name: '关闭', exact: true }).click()
  portFail = false
  await portAction.click()
  await portDialog.getByRole('button', { name: '关闭并启动', exact: true }).click()
  await portDialog.waitFor({ state: 'hidden' })
  assert.equal(portConfirms, 2)
  checks.push('Port action requires valid permission and explicit confirmation; expiry renewal does not close anything; failure consumes consent')
  observed = false
  await page.reload()
  await card.locator('.startup-error summary').click()
  await card.locator('.issue-content').getByText('无法关闭此程序，请在该程序中退出。', { exact: true }).waitFor()
  await noGenericRetry(card)
  assert.equal(await card.getByText('查看占用程序后，可关闭并重新启动。').count(), 0)
  checks.push('Startup failure automatically shows the actual diagnostic reason, retains logs and has no check-again shortcut')
  appStatus = 'running'; runtimeState = 'running'
  observed = false
  await page.reload()
  await restart.click()
  await page.waitForFunction(() => document.querySelector('article.card .run-actions')?.textContent.includes('重启'))
  assert.equal(managed, 1)
  checks.push('Managed project retains direct restart without the external-process confirmation')
  observed = true; kind = 'external'
  await page.reload()
  await page.setViewportSize({ width: 390, height: 740 })
  await restart.click()
  await dialog.getByRole('button', { name: '确认重启', exact: true }).waitFor()
  assert.equal(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth), true)
  await page.screenshot({ path: path.join(evidence, 'mobile-confirmation.png') })
  await dialog.getByRole('button', { name: '取消' }).click()
  checks.push('390px narrow layout remains readable, bounded and keyboard-accessible')
  assert.deepEqual(pageErrors, [])
  const report = { passed: true, boundary: 'Real Vue/browser, simulated API; no user process stopped', checks, confirms, managed, portConfirms, runtimeReads, pageErrors }
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  console.log(JSON.stringify(report, null, 2))
} catch (error) {
  await page?.screenshot({ path: path.join(evidence, 'failure.png') }).catch(() => {})
  throw error
} finally { await browser?.close(); await new Promise(resolve => server.httpServer.close(resolve)) }
