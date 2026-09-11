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
  const { descriptor } = parse(read('src/components/AppCard.vue'))
  const component = compile(compileScript(descriptor, { id: 'app-card-test', inlineTemplate: true }).content, {
    vue, '@/i18n': { tr: text => text }, '@/stores/apps': { useAppsStore: () => ({ load() {} }) },
    '@/api/http': { api: { startupIssue: async () => ({ code: 'port_in_use', ports: [3000], conflicts: [], canRecover: true }) } },
    '@/components/UiIcon.vue': { default: { props: ['name'], setup: p => () => vue.h('svg', { 'data-icon': p.name }) } },
    '@/utils/cardServices': compile(read('src/utils/cardServices.ts')),
    '@/utils/cardColors': compile(read('src/utils/cardColors.ts')),
    '@/stores/motion': { runningEffect: vue.ref('breathe'), startingEffect: vue.ref('sweep') },
    './StartupIndicator.vue': { default: { setup: () => () => vue.h('span') } },
  }).default
  const node = (type, text = '') => ({ type, text, props: {}, children: [], parent: null })
  const renderer = vue.createRenderer({
    createElement: node, createText: text => node('text', text), createComment: () => node('comment'),
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
    assert.equal(nodes.filter(el=>el.props.class==='svc-row').length,2)
    assert.ok(!nodes.some(el=>el.text==='可以重新启动'), 'free port must not make a failed start look successful')
    for (const state of ['checking', 'unknown', 'running', 'conflict']) {
      fixture.runtimeCheck = { state, message: 'Runtime check fixture', conflicts: state === 'conflict' ? [{port:3000,pid:99,name:'other.exe'}] : [] }
      fixture.status = state === 'running' ? 'running' : state === 'conflict' ? 'stopped' : state
      await vue.nextTick()
      const current = all(root)
      const labels = current.filter(el => el.type === 'button').map(el => all(el).map(n => n.text).join(''))
      assert.ok(!labels.some(text => ['启动','停止','重启','重新启动','释放端口并重试'].includes(text)), state + ' must not offer unsafe controls')
      assert.ok(current.some(el => el.props.class === 'runtime-notice'))
      assert.ok(current.some(el => el.type === 'button' && el.props['aria-label'] === '查看日志'))
    }
  } finally { app.unmount();globalThis.document=documentBefore;globalThis.window=windowBefore }
})
