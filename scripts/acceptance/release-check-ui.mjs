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
const dependencies = [
  {kind:'missing',path:'artifacts/before-src/App.jsx',reference:'./icon.svg',missing:'artifacts/before-src/icon.svg',reason:'选中文件引用了不存在的文件',blocked:true},
  {kind:'not_selected',path:'src/main.ts',reference:'./helper',missing:'src/helper.ts',reason:'确定的配套文件未选入',blocked:true},
  {kind:'unresolved',path:'src/main.ts',reference:'./missing',missing:'src/missing',reason:'未找到引用',blocked:true},
]
const files = [
  {path:dependencies[0].path,tracked:false,category:'recommend',reasons:['未跟踪的本地验证资料'],sources:['heuristic','manual']},
  {path:'src/main.ts',tracked:true,category:'recommend',reasons:[],sources:['heuristic']},
  {path:'src/helper.ts',tracked:false,category:'recommend',reasons:[],sources:['heuristic']},
]
const candidate = { id: 'fixture', status: 'unverified', checkResults: checks, warnings: [], sensitiveFindings: [], dependencyFindings: dependencies }
const advisoryCandidate = { ...candidate, status:'passed', accepted:true, canFormal:true, dependencyFindings:dependencies.map(f=>({...f,blocked:false,reason:'未在保存副本中识别此引用；由项目检查和构建确认，不要求补齐推测的文件'})), checkResults:[{id:'rundock:dependencies',name:'文件读取',required:true,status:'passed',reason:'所选文件可读取。引用推断仅供参考。'}] }
await writeFile(path.join(fixtureDir, 'index.html'), '<html lang="zh"><head><meta charset="utf-8"></head><body><div id="app"></div><script type="module" src="/main.ts"></script></body></html>')
const sourceURL = '/@fs/' + root.replaceAll('\\', '/') + '/src'
await writeFile(path.join(fixtureDir, 'main.ts'), `import {createApp,h,ref} from 'vue';import Issues from '${sourceURL}/components/ReleaseCheckIssues.vue';import '${sourceURL}/styles.css';createApp({setup(){const count=ref(0),choices=ref([]),current=ref(${JSON.stringify(candidate)}),selected=ref({'artifacts/before-src/App.jsx':true,'src/main.ts':true});return()=>h('main',{style:'max-width:850px;margin:30px auto;padding:20px'},[h('h2','发布前检查 · 隔离验收'),h(Issues,{appId:'fixture',candidate:current.value,files:${JSON.stringify(files)},selected:selected.value,busy:false,stale:false,onChoose:(file,included)=>{choices.value.push([file.path,included]);selected.value={...selected.value,[file.path]:included}},onCheck:()=>count.value++}),h('output',{id:'retry-count'},String(count.value)),h('output',{id:'choices'},JSON.stringify(choices.value)),h('button',{id:'advisory-only',onClick:()=>current.value=${JSON.stringify(advisoryCandidate)}},'模拟仅有引用提醒')])}}).mount('#app');`)
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
  const dependencyCards=page.locator('.issue-card').filter({has:page.locator('strong', {hasText:'资料不完整'})})
  assert.match(await dependencyCards.innerText(), /引用文件[\s\S]*artifacts\/before-src\/App.jsx[\s\S]*需要文件[\s\S]*icon.svg/)
  assert.equal(await dependencyCards.locator('details').getByText('查看详情').isVisible(),true)
  await dependencyCards.getByRole('button',{name:'跳过此文件',exact:true}).click()
  await page.getByRole('button',{name:'这次保存',exact:true}).click()
  assert.deepEqual(JSON.parse(await page.locator('#choices').innerText()),[['artifacts/before-src/App.jsx',false],['src/helper.ts',true]])
  const unresolved=page.locator('.issue-card').filter({has:page.locator('strong',{hasText:'引用未找到'})})
  assert.match(await unresolved.innerText(),/src\/main.ts[\s\S]*src\/missing[\s\S]*补齐配套文件/)
  assert.equal(await unresolved.locator('button').count(),0,'real source must not receive a skip-material shortcut')
  assert.match(await cards.nth(0).innerText(), /无法检查[\s\S]*pwsh[\s\S]*需要 PowerShell 7/)
  assert.equal(await cards.nth(0).locator('details').count(), 0)
  assert.match(await cards.nth(1).innerText(), /无法检查[\s\S]*必需检查工具不可用[\s\S]*安装检查所需工具/)
  assert.match(await cards.nth(2).innerText(), /检查失败[\s\S]*检查命令失败/)
  assert.equal(await cards.nth(2).locator('pre').isVisible(), false)
  await cards.nth(2).locator('summary').click()
  assert.equal(await cards.nth(2).locator('pre').innerText(), checks[2].log)
  assert.match(await cards.nth(3).innerText(), /没有收到原因或记录/)
  await page.getByRole('button', { name: '重新检查', exact: true }).click()
  assert.equal(await page.locator('#retry-count').innerText(), '1')
  assert.deepEqual(errors, [])
  await page.screenshot({ path: path.join(evidence, 'check-errors.png'), fullPage: true })
  await page.locator('#advisory-only').click()
  assert.equal(await page.locator('.issue-card').count(),0,'reference inference must not be an error')
  assert.equal(await page.getByRole('button',{name:'重新检查',exact:true}).count(),0,'advisories must not require recovery')
  await page.locator('.advisory summary').click()
  assert.match(await page.locator('.advisory').innerText(),/引用参考（不影响发布）[\s\S]*项目检查和构建结果为准/)
  await page.screenshot({ path:path.join(evidence,'reference-advisories.png'),fullPage:true })
  report.passed = true
} finally {
  await writeFile(path.join(evidence, 'report.json'), JSON.stringify(report, null, 2))
  await browser?.close()
  await server.close()
}
