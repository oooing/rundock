import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'
import * as pinia from 'pinia'

const read = path => readFileSync(new URL('../../' + path, import.meta.url), 'utf8')
function compile(source, modules) {
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('exports', 'require', js)(exports, id => { assert.ok(id in modules, id); return modules[id] })
  return exports
}
function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

// Render the real Dashboard and Pinia store with deliberately delayed HTTP.
function mount(t) {
  const previousDocument = globalThis.document, previousWindow = globalThis.window
  globalThis.document = { body: { classList: { remove() {} } } }
  globalThis.window = { removeEventListener() {} }
  const request = deferred()
  const api = { listApps: () => request.promise }
  const modules = { vue, pinia, '@/i18n': { tr: text => text }, '@/api/http': { api },
    '@/api/ws': { wsClient: {} }, '@/utils/cardColors': { pickNextCardColor: () => '' },
    '@/components/UiIcon.vue': { default: { render: () => null } },
    '@/components/AppCard.vue': { default: { props: ['app'], setup: props => () => vue.h('article', { class: 'fixture-card' }, props.app.name) } },
  }
  const store = compile(read('src/stores/apps.ts'), modules).useAppsStore(pinia.createPinia())
  modules['@/utils/useCardDrag'] = compile(read('src/utils/useCardDrag.ts'), { vue, '@/i18n': modules['@/i18n'], './cardDrag': compile(read('src/utils/cardDrag.ts'), {}) })
  const { descriptor } = parse(read('src/views/Dashboard.vue'))
  const Dashboard = compile(compileScript(descriptor, { id: 'dashboard-loading', inlineTemplate: true }).content, modules).default
  const ready = vue.ref(false), groupView = vue.ref(false)
  const node = (type, text = '') => ({ type, text, props: {}, children: [], parent: null })
  const renderer = vue.createRenderer({
    createElement: node, createText: text => node('text', text), createComment: () => node('comment'),
    setText(el, text) { el.text = text }, setElementText(el, text) { el.text = text; el.children = [] },
    patchProp(el, key, old, value) { el.props[key] = value },
    insert(el, parent, anchor) { el.parent = parent; const i = parent.children.indexOf(anchor); parent.children.splice(i < 0 ? parent.children.length : i, 0, el) },
    remove(el) { el.parent.children.splice(el.parent.children.indexOf(el), 1) },
    parentNode: el => el.parent, nextSibling: el => el.parent?.children[el.parent.children.indexOf(el) + 1],
  })
  const root = node('root')
  const app = renderer.createApp({ setup: () => () => vue.h(Dashboard, { apps: store.apps, loading: store.loading,
    ready: ready.value, loadError: store.error, groupView: groupView.value, groups: [], moving: {}, onRetry: store.load }) })
  app.mount(root)
  const all = el => [el, ...el.children.flatMap(all)]
  t.after(() => { app.unmount(); globalThis.document = previousDocument; globalThis.window = previousWindow })
  return { store, api, request, ready, groupView, nodes: () => all(root), text: () => all(root).map(el => el.text).join(' ') }
}

test('connection-to-request gap and delayed response never render an empty-project message', async t => {
  const h = mount(t)
  assert.match(h.text(), /正在连接后台服务/)
  h.ready.value = true
  await vue.nextTick()
  assert.match(h.text(), /正在加载项目/)
  assert.doesNotMatch(h.text(), /把第一个项目|分组暂无项目/)
  const pending = h.store.load()
  await vue.nextTick()
  assert.match(h.text(), /正在加载项目/)
  h.request.resolve([{ id: 'existing', name: 'Saved project' }])
  await pending; await vue.nextTick()
  assert.match(h.text(), /Saved project/)
  assert.doesNotMatch(h.text(), /把第一个项目|分组暂无项目|正在加载项目/)
  const card = h.nodes().find(el => el.type === 'article')
  const refresh = deferred(); h.api.listApps = () => refresh.promise
  const refreshing = h.store.load(); await vue.nextTick()
  assert.equal(h.nodes().find(el => el.type === 'article'), card, 'background refresh must retain the mounted card')
  refresh.resolve([{ id: 'existing', name: 'Saved project' }]); await refreshing
})

test('welcome and empty-group messages appear only after a successful empty response', async t => {
  const h = mount(t); h.ready.value = true; h.groupView.value = true
  const pending = h.store.load(); await vue.nextTick()
  assert.doesNotMatch(h.text(), /把第一个项目|分组暂无项目/)
  h.request.resolve([]); await pending; await vue.nextTick()
  assert.match(h.text(), /分组暂无项目/)
  h.groupView.value = false; await vue.nextTick()
  assert.match(h.text(), /把第一个项目/)
})

test('a failed initial load shows retry instead of an empty welcome, then loads successfully', async t => {
  const h = mount(t); h.ready.value = true
  const pending = h.store.load(); h.request.reject(new Error('Connection lost'))
  await pending; await vue.nextTick()
  assert.match(h.text(), /项目加载失败/)
  assert.doesNotMatch(h.text(), /把第一个项目|分组暂无项目/)
  const retry = deferred(); h.api.listApps = () => retry.promise
  const clicked = h.nodes().find(el => el.type === 'button').props.onClick()
  await vue.nextTick(); assert.match(h.text(), /正在加载项目/)
  retry.resolve([{ id: 'existing', name: 'Restored project' }]); await clicked; await vue.nextTick()
  assert.match(h.text(), /Restored project/)
})
