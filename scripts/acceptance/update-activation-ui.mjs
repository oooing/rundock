// Actual Vue sidebar, settings and dialog; only native IPC/project data are mocked.
// No installer, production backend or running project is contacted.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.RUNDOCK_PLAYWRIGHT_MODULE || 'playwright')
const root = process.cwd(), dir = fs.mkdtempSync(path.join(root, '.tmp/update-activation-ui-'))
const out = path.join(root, 'outputs/acceptance/update-activation')
fs.mkdirSync(out, { recursive: true })
fs.writeFileSync(path.join(dir, 'index.html'), '<html><body><div id="app"></div><script type="module" src="/main.js"></script></body></html>')
fs.writeFileSync(path.join(dir, 'native.js'), `
export class Channel {}
export async function invoke(cmd, args) {
 window.calls.push({cmd,args});
 if(cmd==='app_update_status') return structuredClone(window.report);
 if(cmd==='update_install_plan') return {fingerprint:'fixture', projects:[{id:'a',name:'保留运行的项目'}]};
 throw Error('Unexpected native action: '+cmd);
}`)
fs.writeFileSync(path.join(dir, 'window.js'), `export const isTauri=true;export const getAppVersion=async()=>'2.1.0';export const openProjectGitHub=async()=>{};export const openProjectReleases=async()=>{};`)
fs.writeFileSync(path.join(dir, 'projects.js'), `export const useAppsStore=()=>({apps:[],loading:false});export const useGroupsStore=()=>({groups:[]});`)
fs.writeFileSync(path.join(dir, 'main.js'), `
import {createApp,h} from 'vue';
import Dialog from '@/components/AppUpdateDialog.vue';
import Sidebar from '@/components/GroupSidebar.vue';
import Panel from '@/components/AppUpdatePanel.vue';
import * as store from '@/stores/appUpdate';import {setLocale} from '@/i18n';import '@/styles.css';
setLocale('zh-CN');window.store=store;window.setLocale=setLocale;window.calls=[];
window.report={version:'2.1.0',phase:'staged',projects:[],remaining:[],error:'',failures:[],currentVersion:'2.1.0',backendVersion:'2.0.49',versionActive:false,activationState:'backend_pending'};
createApp({render:()=>h('div',{style:'display:flex;min-height:100vh'},[h(Sidebar,{selected:null}),h('main',{style:'flex:1;padding:28px'},[h(Panel,{currentVersion:'2.1.0'}),store.appUpdate.dialogOpen?h(Dialog):null])])}).mount('#app');
await store.prepareStartupUpdate();
`)
const server = await createServer({ configFile: false, root: dir, plugins: [vue()], resolve: { alias: [
  { find: '@/tauri/window', replacement: path.join(dir, 'window.js') },
  { find: '@/stores/apps', replacement: path.join(dir, 'projects.js') },
  { find: '@/stores/groups', replacement: path.join(dir, 'projects.js') },
  { find: '@tauri-apps/api/core', replacement: path.join(dir, 'native.js') },
  { find: '@', replacement: path.join(root, 'src') },
] }, server: { host: '127.0.0.1', port: 0, fs: { allow: [root] } } })
let browser
try {
  await server.listen()
  browser = await chromium.launch({ channel: 'msedge', headless: true })
  const page = await browser.newPage({ locale: 'zh-CN', viewport: { width: 1000, height: 760 } })
  const errors = []
  page.on('pageerror', e => errors.push(e.message))
  await page.goto(server.resolvedUrls.local[0])
  await page.locator('.update-link').getByText('后台待更新', { exact: true }).waitFor()
  assert.match(await page.locator('.app-update').innerText(), /窗口已更新/)
  assert.doesNotMatch(await page.locator('body').innerText(), /下次启动生效|更新完成/)
  await page.locator('.update-link').click()
  await page.getByRole('heading', { name: '后台待更新', exact: true }).waitFor()
  assert.match(await page.getByRole('dialog').innerText(), /2\.0\.49/)
  await page.screenshot({ path: path.join(out, 'backend-pending.png') })
  await page.getByRole('button', { name: '立即生效', exact: true }).click()
  await page.getByText('保留运行的项目', { exact: true }).waitFor()
  assert.match(await page.getByRole('dialog').innerText(), /停止以下项目/)
  assert.equal(await page.evaluate(() => window.calls.some(c => c.cmd === 'install_app_update')), false)
  await page.getByRole('button', { name: '返回', exact: true }).click()
  await page.getByRole('button', { name: '知道了', exact: true }).click()
  await page.evaluate(async () => { window.report.activationState = 'unconfirmed'; window.report.backendVersion = null; await window.store.refreshUpdateStatus({background:true}) })
  await page.locator('.update-link').getByText('状态未知', { exact: true }).waitFor()
  await page.locator('.update-link').click()
  assert.equal(await page.getByRole('button', { name: '立即生效', exact: true }).count(), 0)
  assert.match(await page.getByRole('dialog').innerText(), /暂时无法确认后台版本/)
  await page.setViewportSize({ width: 390, height: 700 })
  const box = await page.getByRole('dialog').boundingBox()
  assert.ok(box.x >= 0 && box.x + box.width <= 390)
  await page.screenshot({ path: path.join(out, 'unconfirmed-mobile.png') })
  await page.evaluate(() => window.setLocale('en'))
  await page.getByRole('heading', { name: 'Status unknown', exact: true }).waitFor()
  assert.doesNotMatch(await page.getByRole('dialog').innerText(), /[\u4e00-\u9fff]/)
  await page.evaluate(async () => { window.report = null; await window.store.refreshUpdateStatus({background:true}) })
  assert.equal(await page.locator('.update-link').count(), 0)
  assert.equal(await page.evaluate(() => window.store.appUpdate.phase), 'current')
  assert.deepEqual(errors, [])
  const calls = await page.evaluate(() => window.calls)
  assert.ok(calls.every(c => ['app_update_status', 'update_install_plan'].includes(c.cmd)))
  fs.writeFileSync(path.join(out, 'result.json'), JSON.stringify({ passed: true, boundary: 'actual Vue components; native IPC and projects mocked; no real install or project writes', checks: ['kept old backend is distinct from pending window restart across all three surfaces', 'activation requires a project list and explicit confirmation', 'unconfirmed backend never claims success or offers activation', 'verified completion removes sidebar reminder', 'Chinese, English and 390px dialog'], calls }, null, 2))
  console.log('PASS: update activation UI, confirmation boundary, cleared reminder, localization and narrow dialog.')
} finally {
  await browser?.close(); await server.close()
  const tempRoot = path.resolve(root, '.tmp') + path.sep
  if (path.resolve(dir).startsWith(tempRoot)) fs.rmSync(dir, { recursive: true, force: true })
}
