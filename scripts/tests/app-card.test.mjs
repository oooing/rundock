import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'

function compile(source, modules = {}) {
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('exports', 'require', js)(exports, id => { assert.ok(id in modules, id); return modules[id] })
  return exports
}

test('failed card has a direct icon button for this project log, outside collapsed details', async () => {
  const documentBefore = globalThis.document, windowBefore = globalThis.window
  globalThis.document = { addEventListener() {}, removeEventListener() {} }
  globalThis.window = { addEventListener() {}, removeEventListener() {} }
  const calls = []
  const read = path => readFileSync(new URL('../../' + path, import.meta.url), 'utf8')
  const common = {
    vue, '@/i18n': { tr: (text, values = []) => text.replace(/\{(\d+)\}/g, (_, index) => String(values[index])) },
    '@/stores/apps': { useAppsStore: () => ({ operationBusy: {}, load() {} }) },
    '@/api/http': { api: { startupIssue: async () => ({ code: 'port_in_use', ports: [3000], conflicts: [], canRecover: true }) } },
  }
  const icon = { default: { props: ['name'], setup: p => () => vue.h('svg', { 'data-icon': p.name }) } }
  const notice = parse(read('src/components/AppStartupNotice.vue')).descriptor
  const noticeComponent = compile(compileScript(notice, { id: 'startup-notice-test', inlineTemplate: true }).content, {
    ...common, './UiIcon.vue': icon,
    '@/utils/serviceHealth': compile(read('src/utils/serviceHealth.ts'), common),
  }).default
  const { descriptor } = parse(read('src/components/AppCard.vue'))
  const component = compile(compileScript(descriptor, { id: 'app-card-test', inlineTemplate: true }).content, {
    ...common, '@/components/UiIcon.vue': icon,
    '@/composables/useAppStartup': compile(read('src/composables/useAppStartup.ts'), common),
    './AppStartupNotice.vue': { default: noticeComponent },
    './PortResolutionDialog.vue': { default: { setup: () => () => null } },
    './RestartDialog.vue': { default: { setup: () => () => null } },
    '@/utils/cardServices': compile(read('src/utils/cardServices.ts')),
    '@/utils/cardColors': compile(read('src/utils/cardColors.ts')),
    '@/stores/motion': { runningEffect: vue.ref('breathe'), startingEffect: vue.ref('sweep') },
    './StartupIndicator.vue': { default: { setup: () => () => vue.h('span') } },
  }).default
  const node = (type, text = '') => ({ type, text, props: {}, children: [], parent: null })
  const renderer = vue.createRenderer({
    createElement: type => node(type), createText: text => node('text', text), createComment: () => node('comment'),
    setText(el,text) { el.text=text }, setElementText(el,text) { el.text=text;el.children=[] },
    patchProp(el,key,old,value) { el.props[key]=value },
    insert(el,parent,anchor) { el.parent=parent;const i=parent.children.indexOf(anchor);parent.children.splice(i<0?parent.children.length:i,0,el) },
    remove(el) { el.parent.children.splice(el.parent.children.indexOf(el),1) },
    parentNode: el=>el.parent, nextSibling: el=>el.parent?.children[el.parent.children.indexOf(el)+1],
  })
  const fixture = vue.reactive({ id: 'failed-project', name: 'Fixture', status: 'failed',
    cardColor: '', entryScript: 'C:\\fixture\\start.bat', services: [], portHints: [3000,8000], lastUrl: 'http://localhost:3000' })
  const app = renderer.createApp(component, { app: fixture,
    groups: [], onLog: id => calls.push(id) })
  try {
    const root=node('root');app.mount(root)
    await Promise.resolve();await vue.nextTick()
    const all=el=>[el,...el.children.flatMap(all)], nodes=all(root)
    const direct=nodes.find(el=>el.type==='button'&&el.props.class==='failure-log-link')
    assert.ok(direct)
    for(let p=direct.parent;p;p=p.parent)assert.notEqual(p.type,'details')
    assert.ok(all(direct).some(el=>el.props['data-icon']==='alert-circle'))
    assert.ok(all(direct).some(el=>el.text==='查看失败日志'))
    direct.props.onClick()
    assert.deepEqual(calls,['failed-project'])
    assert.ok(nodes.some(el=>el.type==='button'&&el.props['aria-label']==='查看日志'), 'regular log icon remains available')
    assert.ok(nodes.some(el=>el.text==='启动脚本'))
    assert.equal(nodes.filter(el=>el.props.class==='svc-row').length,0, 'configured hints are not discovered services')
    assert.ok(!nodes.some(el=>el.text==='可以重新启动'), 'free port must not make a failed start look successful')
    const service = (port, role = 'unknown') => ({ id: String(port), port, role, url: `http://localhost:${port}`, health: 'healthy' })
    const liveServices = [service(9100, 'frontend'), service(18009, 'backend'), service(8081)]
    fixture.knownServices = [service(8009, 'backend'), ...liveServices]
    fixture.portHints = [18009, 9100, 8081, 7890]
    for (const status of ['starting', 'running', 'degraded', 'stopping']) {
      fixture.status = status
      fixture.services = liveServices
      await vue.nextTick()
      const rendered = all(root)
      const rows = rendered.filter(el => el.props.class === 'svc-row')
      assert.equal(rows.length, 3, status + ' counts only current services')
      const heading = rendered.find(el => el.props.class === 'services-heading')
      assert.ok(all(heading).some(el => el.text === '3'))
      const details = rendered.find(el => el.props.class === 'runtime-details')
      const detailNodes = all(details), detailText = detailNodes.map(el => el.text).join(' ')
      assert.match(detailText, /8009/)
      assert.match(detailText, /7890/)
      assert.ok(!detailNodes.some(el => el.type === 'a'), 'historical and candidate ports are not live links')
      fixture.services = []
      await vue.nextTick()
      assert.equal(all(root).filter(el => el.props.class === 'svc-row').length, 0, status + ' must not fall back to stale ports')
    }
    fixture.status = 'degraded'
    fixture.services = [{ ...service(17655, 'backend'), health: 'unhealthy', healthReason: 'http_status:503', healthProbeUrl: 'http://localhost:17655/api/health' },
      { ...service(5284), health: 'unhealthy', statusScope: 'auxiliary', healthReason: 'http_status:400' }]
    await vue.nextTick()
    const healthNotice = all(root).find(el => el.props.class === 'health-notice')
    assert.equal(healthNotice.props.role, 'status')
    const noticeText = all(healthNotice).map(el => el.text).join(' ')
    assert.match(noticeText, /17655.*HTTP 503/)
    assert.doesNotMatch(noticeText, /5284|HTTP 400/)
    all(healthNotice).find(el => el.type === 'button').props.onClick()
    assert.deepEqual(calls, ['failed-project', 'failed-project'])
    fixture.status = 'running'
    await vue.nextTick()
    assert.ok(!all(root).some(el => el.props.class === 'health-notice'), 'recovery removes the warning')
    fixture.services = []
    fixture.status = 'stopped'
    await vue.nextTick()
    const stoppedRows = all(root).filter(el => el.props.class === 'svc-row')
    assert.equal(stoppedRows.length, 4, 'last-known addresses remain available after stopping')
    assert.ok(stoppedRows.every(el => all(el).some(n => n.text === '上次发现')))
    for (const state of ['checking', 'unknown', 'running', 'conflict']) {
      fixture.runtimeCheck = { state, message: 'Runtime check fixture', conflicts: state === 'conflict' ? [{port:3000,pid:99,name:'other.exe'}] : [] }
      fixture.status = state === 'running' ? 'running' : state === 'conflict' ? 'stopped' : state
      await vue.nextTick()
      const current = all(root)
      const labels = current.filter(el => el.type === 'button').map(el => all(el).map(n => n.text).join('').trim())
      const unsafe = state === 'running' ? ['启动', '停止', '重新启动', '释放端口并重试'] : ['启动', '停止', '重启', '重新启动', '释放端口并重试']
      assert.ok(!labels.some(text => unsafe.includes(text)), state + ' must not offer unsafe controls')
      if (state === 'running') assert.ok(labels.includes('重启'), 'observed running projects retain the verified restart dialog entry')
      assert.ok(current.some(el => el.props.class === 'runtime-notice'))
      assert.ok(current.some(el => el.type === 'button' && el.props['aria-label'] === '查看日志'))
    }
  } finally { app.unmount();globalThis.document=documentBefore;globalThis.window=windowBefore }
})
