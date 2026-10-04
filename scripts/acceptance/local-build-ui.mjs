import assert from 'node:assert/strict'
import path from 'node:path'

// Failure inventory before the UI refactor: a third navigation tab survives;
// cloud is not the initial mode; switching modes creates/cancels tasks; a closed
// dialog loses the accepted task; no-Git or broken config hides retained outputs.
export async function openCurrentLocalBuild(page) {
  assert.equal(await page.getByRole('tab', { name: '本地构建', exact: true }).count(), 0)
  await page.getByRole('radio', { name: '本地构建', exact: true }).check()
  await page.getByRole('checkbox', { name: '仅构建当前版本', exact: true }).check()
  await page.locator('#release-panel-local-build').waitFor()
}

export async function verifyLocalBuildInteraction({ page, context, base, uiBase, evidence, report, prepareLongHistory }) {
  const panel = page.locator('#release-panel-local-build')
  let verifiedRefreshLock = false
  async function choose(target) {
    const pattern = `${base}/api/apps/*/local-builds`
    // A delayed real response reproduces selection being overwritten mid-click.
    // The UI must prevent edits until this configuration refresh settles.
    const delayRefresh = async route => {
      if (route.request().method() === 'GET') await new Promise(resolve => setTimeout(resolve, 700))
      await route.continue()
    }
    if (!verifiedRefreshLock) await page.route(pattern, delayRefresh)
    await page.getByRole('button', { name: '新建构建', exact: true }).click()
    await panel.locator('fieldset').waitFor()
    if (!verifiedRefreshLock) assert.ok(await panel.getByRole('checkbox', { name: /^Updated fixture target/ }).isDisabled())
    await panel.locator('input[type=checkbox]:checked').uncheck()
    await panel.getByRole('checkbox', { name: target }).check()
    if (!verifiedRefreshLock) {
      await page.unroute(pattern, delayRefresh)
      verifiedRefreshLock = true
      report.checks.push('Delayed real configuration response locks target editing until loaded; a selected target is not overwritten mid-click')
    }
  }
  await choose(/^Fixture slow/)
  await panel.getByRole('button', { name: '开始构建', exact: true }).click()
  await panel.getByRole('button', { name: '取消构建', exact: true }).waitFor()
  const cancelPattern = `${base}/api/local-builds/*/cancel`
  await page.route(cancelPattern, route => route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'fixture cancel rejected' }) }))
  await panel.getByRole('button', { name: '取消构建', exact: true }).click()
  await panel.getByText('fixture cancel rejected', { exact: true }).waitFor()
  await page.waitForTimeout(1800)
  assert.ok(await panel.getByText('fixture cancel rejected', { exact: true }).isVisible())
  await page.unroute(cancelPattern)
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByText('closed', { exact: true }).waitFor()
  await page.getByRole('button', { name: 'Open build', exact: true }).click()
  await openCurrentLocalBuild(page)
  await panel.getByRole('button', { name: '取消构建', exact: true }).click()
  await panel.locator('.local-build-status').filter({ hasText: '已取消构建' }).waitFor()
  report.checks.push('UI close/reopen recovers running task; explicit cancel gives terminal feedback')

  await choose(/^Fixture noisy/)
  await panel.getByRole('button', { name: '开始构建', exact: true }).click()
  await panel.locator('.local-build-status').filter({ hasText: '本地构建失败' }).waitFor()
  const failedId = await page.evaluate(() => Object.entries(localStorage).find(([key]) => key.startsWith('rundock.local-build.view.'))?.[1])
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await prepareLongHistory(failedId)
  await page.getByRole('button', { name: 'Open build', exact: true }).click()
  await openCurrentLocalBuild(page)
  await panel.locator('.local-build-status').filter({ hasText: '本地构建失败' }).waitFor()
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: uiBase })
  await panel.getByRole('button', { name: '复制错误信息', exact: true }).click()
  await panel.getByText('已复制', { exact: true }).waitFor()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  assert.match(copied, /local_build_step_failed/)
  assert.match(copied, /noisy-tail-marker: compiler failed at the end/)
  await page.screenshot({ path: path.join(evidence, 'local-build-failure-action.png'), fullPage: true })
  report.checks.push('Reopened real compiler failure after injecting 650 persisted history records: copies final diagnostics instead of the first log page')

  await choose(/^Updated fixture target/)
  const createPattern = `${base}/api/apps/*/local-builds`
  await page.route(createPattern, route => route.request().method() === 'POST'
    ? route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ error: 'fixture formal release in progress' }) })
    : route.continue())
  await panel.getByRole('button', { name: '开始构建', exact: true }).click()
  await panel.getByText('fixture formal release in progress', { exact: true }).waitFor()
  await page.waitForTimeout(1500)
  assert.ok(await panel.getByText('fixture formal release in progress', { exact: true }).isVisible())
  await page.unroute(createPattern)
  report.checks.push('UI 409 creation error and cancel error survive background refresh until a deliberate retry')
  let acceptedId = '', intercepted = 0
  const routePattern = `${base}/api/apps/*/local-builds`
  const interceptor = async route => {
    if (route.request().method() !== 'POST' || intercepted++) return route.continue()
    const accepted = await route.fetch()
    assert.ok(accepted.ok())
    acceptedId = (await accepted.json()).id
    await route.abort('failed') // Real server accepted; browser lost only the response.
  }
  await page.route(routePattern, interceptor)
  await panel.getByRole('button', { name: '开始构建', exact: true }).click()
  await panel.getByRole('button', { name: '确认任务状态', exact: true }).waitFor()
  await page.unroute(routePattern, interceptor)
  await panel.getByRole('button', { name: '确认任务状态', exact: true }).click()
  await panel.locator('.local-build-status').filter({ hasText: '本地构建完成' }).waitFor()
  const stored = await page.evaluate(() => Object.entries(localStorage).find(([key]) => key.startsWith('rundock.local-build.view.'))?.[1])
  assert.equal(stored, acceptedId)
  report.checks.push('Lost accepted HTTP response: UI retains idempotency key and recovers the same task without duplicate commands')
}
