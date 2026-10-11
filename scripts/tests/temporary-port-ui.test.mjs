import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'

const tr = (text, values = []) => text.replace(/\{(\d+)\}/g, (_, index) => String(values[index]))
function component(api, store) {
  const { descriptor } = parse(readFileSync(new URL('../../src/components/PortResolutionDialog.vue', import.meta.url), 'utf8'))
  const code = ts.transpileModule(compileScript(descriptor, { id: 'port-test', inlineTemplate: true }).content,
    { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const modules = { vue, '@/api/http': { api }, '@/stores/apps': { useAppsStore: () => store }, '@/i18n': { tr } }
  const exports = {}
  new Function('exports', 'require', code)(exports, id => { assert.ok(id in modules, id); return modules[id] })
  return exports.default
}
const node = (type, text = '') => ({ type, text, props: {}, children: [], parent: null, open: false,
  showModal() { this.open = true }, close() { this.open = false } })
function renderer() {
  return vue.createRenderer({ createElement: type => node(type), createText: text => node('text', text), createComment: () => node('comment'),
    setText(el, text) { el.text = text }, setElementText(el, text) { el.text = text; el.children = [] },
    patchProp(el, key, old, value) { el.props[key] = value },
    insert(el, parent, anchor) { if (el.parent) { const i=el.parent.children.indexOf(el); if(i>=0)el.parent.children.splice(i,1) }; el.parent = parent; const i=parent.children.indexOf(anchor); parent.children.splice(i<0?parent.children.length:i,0,el) },
    remove(el) { el.parent.children.splice(el.parent.children.indexOf(el),1) }, parentNode: el => el.parent, nextSibling: el => el.parent?.children[el.parent.children.indexOf(el)+1] })
}
const all = el => [el, ...el.children.flatMap(all)]
const text = el => all(el).map(n => n.text).join(' ')
const supported = { state: 'conflict', message: '端口被其他程序占用，确认关闭后将自动启动项目',
  canResolve: true, conflicts: [{ port: 3000, pid: 1, name: 'node.exe', managedAppName: 'A', canClose: true }], reservedPorts: [],
  confirmationToken: 'close-token', expiresAt: new Date(Date.now()+60000).toISOString(),
  temporaryPort: { supported: true, originalPort: 3000, port: 43001, confirmationToken: 'temporary-token', expiresAt: new Date(Date.now()+60000).toISOString() } }

test('temporary port is an explicit independent action, shows boundaries, and blocks double submission', async () => {
  const calls=[],store={operationBusy:vue.reactive({}),load:async()=>{},checkRuntime:async()=>{}}
  let done
  const api={portResolution:async()=>supported,startTemporaryPort:async(...args)=>{calls.push(['temporary',...args]);await new Promise(resolve=>{done=resolve})},resolvePorts:async(...args)=>calls.push(['close',...args])}
  const root=node('root'),app=renderer().createApp(component(api,store),{appId:'B',appName:'B'}),vm=app.mount(root)
  try {
    await vm.open();await vue.nextTick()
    assert.match(text(root),/3000.*43001/)
    assert.match(text(root),/不修改原配置，不停止占用程序/)
    assert.match(text(root),/旧数据不会删除/)
    const button=all(root).find(n=>n.type==='button'&&text(n).includes('临时换空闲端口并启动'))
    assert.ok(button);assert.equal(button.props.class,'primary')
    const close=all(root).find(n=>n.type==='button'&&text(n).includes('关闭占用程序并启动'))
    assert.ok(close);assert.notEqual(close.props.class,'primary')
    const request=button.props.onClick();await vue.nextTick()
    await button.props.onClick()
    assert.deepEqual(calls,[['temporary','B','temporary-token']])
    assert.equal(store.operationBusy.B,true)
    assert.match(text(root),/正在临时换端口启动；占用程序保持运行/)
    assert.doesNotMatch(text(root),/暂不支持临时换端口/)
    done();await request;await vue.nextTick()
    assert.equal(store.operationBusy.B,undefined)
    assert.equal(root.children[0].open,false)
  } finally { app.unmount() }
})

test('unsupported and expired plans explain why and never offer an executable temporary action', async () => {
  for(const mode of ['unsupported','expired','old-backend']) {
    const plan={...supported,temporaryPort:mode==='old-backend'?undefined:mode==='unsupported'?{supported:false,reason:'接口兼容性未声明'}:{...supported.temporaryPort,expiresAt:'2000-01-01T00:00:00Z'}}
    const root=node('root'),store={operationBusy:{},load:async()=>{},checkRuntime:async()=>{}}
    const app=renderer().createApp(component({portResolution:async()=>plan},store),{appId:'B',appName:'B'}),vm=app.mount(root)
    try {
      await vm.open();await vue.nextTick()
      assert.ok(!all(root).some(n=>n.type==='button'&&text(n).includes('临时换空闲端口并启动')))
      assert.match(text(root),mode==='expired'?/已过期/:mode==='unsupported'?/接口兼容性未声明/:/后台未提供端口兼容信息/)
      if(mode==='expired') assert.ok(all(root).some(n=>n.type==='button'&&text(n).includes('更新确认信息')))
    } finally { app.unmount() }
  }
})
