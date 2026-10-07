// Real Vue/browser interaction with isolated API fixtures; never mutates user projects.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { preview } from 'vite'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const evidence = path.join(root, 'outputs/acceptance/group-management')
mkdirSync(evidence, { recursive: true })
const server = await preview({ root, preview: { host: '127.0.0.1', port: 0, strictPort: false } })
let browser
const checks = []
try {
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  const page = await browser.newPage({ locale: 'zh-CN', viewport: { width: 1200, height: 800 } })
  let groups = [{ id: 'dev', name: 'DEV', color: '' }, { id: 'other', name: 'Other', color: '' }]
  const apps = [{ id: 'fixture', name: 'Fixture project', entryScript: 'C:/fixture/start.cmd', cwd: 'C:/fixture', args: [], env: {}, tags: [], services: [], portHints: [], status: 'stopped', groupId: 'dev', adapterType: 'batch' }]
  let failRename = false, failDelete = false, failMove = false
  const mutations = []
  await page.addInitScript(() => { window.__LAUNCHER_BASE__ = 'http://fixture.invalid'; localStorage.setItem('rundock.ui.locale', 'zh-CN') })
  await page.route('http://fixture.invalid/**', async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method()
    const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' }
    const send = (body, status = 200) => route.fulfill({ status, headers, contentType: 'application/json', body: JSON.stringify(body) })
    if (method === 'OPTIONS') return send({})
    if (method === 'GET') {
      if (url.pathname === '/api/health') return send({ apiVersion: '2', capabilities: 'release-v2' })
      if (url.pathname === '/api/groups') return send(groups)
      if (url.pathname === '/api/apps') return send(apps)
      if (url.pathname === '/api/cloud-builds') return send([])
      return send({})
    }
    const body = request.postDataJSON()
    if (url.pathname === '/api/groups/dev' && method === 'PATCH') {
      if (failRename) return send({ error: 'Rename rejected' }, 500)
      groups[0].name = body.name; mutations.push('rename'); return send(groups[0])
    }
    if (url.pathname === '/api/groups/dev' && method === 'DELETE') {
      if (failDelete) return send({ error: 'Delete rejected' }, 500)
      groups = groups.filter(g => g.id !== 'dev'); apps[0].groupId = ''; mutations.push('delete'); return send({ deleted: true })
    }
    if (url.pathname === '/api/apps/fixture' && method === 'PATCH') {
      if (failMove) return send({ error: 'Move rejected' }, 500)
      Object.assign(apps[0], body); mutations.push('move'); return send(apps[0])
    }
    throw Error(`Unexpected mutation ${method} ${url.pathname}`)
  })
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}`, { waitUntil: 'domcontentloaded' })
  const sidebar = page.locator('.sidebar')
  await sidebar.getByRole('button', { name: 'DEV' }).click()
  assert.equal(await page.getByRole('button', { name: '添加项目', exact: true }).count(), 1)
  const heading = page.locator('.group-heading')
  await heading.getByRole('button', { name: 'DEV', exact: true }).click()
  await heading.getByRole('textbox', { name: '分组名称' }).fill('Draft')
  await heading.getByRole('textbox').press('Escape')
  assert.equal(mutations.length, 0)
  await heading.getByRole('button', { name: 'DEV', exact: true }).click()
  await heading.getByRole('textbox').fill('研发')
  await heading.getByRole('textbox').dispatchEvent('keydown', { key: 'Enter', isComposing: true })
  assert.equal(mutations.length, 0)
  await heading.getByRole('textbox').press('Enter')
  await heading.getByRole('button', { name: '研发', exact: true }).waitFor()
  await sidebar.getByRole('button', { name: '研发' }).waitFor()
  checks.push('One Add Project button; click rename, Escape cancellation, IME safety and Enter persistence')
  failRename = true
  await heading.getByRole('button', { name: '研发', exact: true }).click()
  await heading.getByRole('textbox').fill('Rejected')
  await heading.getByRole('textbox').press('Enter')
  await heading.getByRole('alert').filter({ hasText: 'Rename rejected' }).waitFor()
  await heading.getByRole('textbox').press('Escape')
  failRename = false
  const card = page.locator('article.card')
  await card.locator('.manage > summary').click()
  assert.equal(await card.getByRole('combobox', { name: '更改分组' }).inputValue(), 'dev')
  failMove = true
  await card.getByRole('combobox').selectOption('other')
  await page.getByText(/移动分组失败：Move rejected/).waitFor()
  assert.equal(await card.getByRole('combobox').inputValue(), 'dev')
  failMove = false
  await card.getByRole('combobox').selectOption('other')
  await sidebar.getByRole('button', { name: 'Other' }).click()
  await card.locator('.manage > summary').click()
  assert.equal(await card.getByRole('combobox').inputValue(), 'other')
  await card.getByRole('combobox').selectOption('dev')
  await sidebar.getByRole('button', { name: '研发' }).click()
  checks.push('Card menu shows current group; move updates filtered views and failed move retains original group')
  await heading.getByLabel('管理分组').click()
  await heading.getByRole('button', { name: '删除分组', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '删除分组' })
  await dialog.getByRole('button', { name: '取消' }).click()
  assert.ok(!mutations.includes('delete'))
  await heading.getByLabel('管理分组').click()
  await heading.getByRole('button', { name: '删除分组', exact: true }).click()
  failDelete = true
  await dialog.getByRole('button', { name: '确认删除' }).click()
  await dialog.getByRole('alert').filter({ hasText: 'Delete rejected' }).waitFor()
  assert.ok(!mutations.includes('delete'))
  failDelete = false
  await dialog.getByRole('button', { name: '确认删除' }).click()
  await page.getByRole('heading', { name: '未分组', exact: true }).waitFor()
  await card.getByRole('button', { name: 'Fixture project', exact: true }).waitFor()
  assert.equal(await sidebar.getByRole('button', { name: '研发' }).count(), 0)
  assert.equal(await page.getByLabel('管理分组').count(), 0)
  checks.push('Delete confirmation can cancel; failed delete is retryable; successful delete moves projects to Ungrouped without stop/delete requests')
  await page.screenshot({ path: path.join(evidence, 'passed.png') })
  console.log(JSON.stringify({ passed: true, boundary: 'Current Vue + browser; isolated simulated API, no user data touched', checks }, null, 2))
  writeFileSync(path.join(evidence, 'report.json'), JSON.stringify({ passed: true, checks }, null, 2))
} finally { await browser?.close(); await new Promise(resolve => server.httpServer.close(resolve)) }
