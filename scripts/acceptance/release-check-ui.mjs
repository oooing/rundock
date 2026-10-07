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
const fixtureDir = path.join(root, '.tmp/release-check-ui')
const evidence = path.join(root, 'outputs/release-check-ui')
await mkdir(fixtureDir, { recursive: true })
await mkdir(evidence, { recursive: true })
const checks = [
  { id: 'pc', name: 'PC 客户端', status: 'unverified', reason: '找不到检查工具：pwsh（PowerShell 7）。RunDock 当前进程的 PATH 无法定位该工具，检查尚未执行。' },
  { id: 'legacy', name: '旧后端结果', status: 'unverified', reason: '必需检查工具不可用' },
  { id: 'tests', name: '单元测试', status: 'failed', reason: '检查命令失败', log: 'AssertionError: expected 2, received 1' },
  { id: 'unknown', name: '未知错误', status: 'unverified' },
].map(check => ({ ...check, required: true }))
const candidate = { id: 'fixture', status: 'unverified', checkResults: checks, warnings: [], sensitiveFindings: [], dependencyFindings: [] }
await writeFile(path.join(fixtureDir, 'index.html'), '<html lang="zh"><head><meta charset="utf-8"></head><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
const sourceURL = '/@fs/' + root.replaceAll('\\', '/') + '/src'
await writeFile(path.join(fixtureDir, 'main.ts'), `import {createApp,h,ref} from 'vue';import Issues from '${sourceURL}/components/ReleaseCheckIssues.vue';import '${sourceURL}/styles.css';createApp({setup(){const count=ref(0);return()=>h('main',{style:'max-width:850px;margin:30px auto;padding:20px'},[h('h2','发布前检查 · 隔离验收'),h(Issues,{appId:'fixture',candidate:${JSON.stringify(candidate)},files:[],selected:{},busy:false,stale:false,onCheck:()=>count.value++}),h('output',{id:'retry-count'},String(count.value))])}}).mount('#app');`)
const server = await createServer({ configFile: false, root: fixtureDir, plugins: [vue()], resolve: { alias: { '@': path.join(root, 'src') } }, optimizeDeps: { entries: ['index.html'] }, server: { host: '127.0.0.1', port: 19483, strictPort: false, fs: { allow: [root] } } })
let browser
const report = { passed: false, boundary: 'real Vue component; fixture results; no backend, builds or release calls' }
try {
  await server.listen()
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  const page = await browser.newPage({ viewport: { width: 1100, height: 950 } })
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/`)
  const cards = page.locator('.check-failure')
  await cards.nth(3).waitFor()
  assert.match(await cards.nth(0).innerText(), /无法开始检查[\s\S]*pwsh[\s\S]*安装 PowerShell 7/)
  assert.equal(await cards.nth(0).locator('details').count(), 0)
  assert.match(await cards.nth(1).innerText(), /必需检查工具不可用[\s\S]*PATH/)
  assert.match(await cards.nth(2).innerText(), /检查失败[\s\S]*检查命令失败/)
  assert.equal(await cards.nth(2).locator('pre').isVisible(), false)
  await cards.nth(2).getByText('执行日志', { exact: true }).click()
  assert.equal(await cards.nth(2).locator('pre').innerText(), checks[2].log)
  assert.match(await cards.nth(3).innerText(), /没有执行日志/)
  await page.getByRole('button', { name: '重新检查', exact: true }).click()
  assert.equal(await page.locator('#retry-count').innerText(), '1')
  assert.deepEqual(errors, [])
  await page.screenshot({ path: path.join(evidence, 'check-errors.png'), fullPage: true })
  report.passed = true
} finally {
  await writeFile(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  await browser?.close()
  await server.close()
}
