import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as vue from 'vue'

// Mount the real SFCs with Vue's renderer; replace only dependencies unrelated
// to navigation. No browser or OS is launched by this regression test.
function mount(name, native, open) {
  const { descriptor } = parse(readFileSync(new URL(`../../src/components/${name}.vue`, import.meta.url), 'utf8'))
  const source = compileScript(descriptor, { id: name, inlineTemplate: true }).content
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const modules = {
    vue,
    '@/i18n': { tr: s => s },
    '@/stores/groups': { useGroupsStore: () => ({ groups: [] }) },
    '@/stores/apps': { useAppsStore: () => ({ apps: [] }) },
    '@/components/UiIcon.vue': { default: { render: () => null } },
    '../../package.json': { default: { version: '2.0.14' } },
    '@/stores/appUpdate': { appUpdate: vue.reactive({ phase: 'idle', error: 'Test error' }) },
    '@/tauri/window': { isTauri: native, getAppVersion: async () => '2.0.14', openProjectGitHub: open, openProjectReleases: open },
  }
  const exports = {}
  new Function('require', 'exports', compiled)(id => {
    assert.ok(id in modules, `Unexpected dependency: ${id}`)
    return modules[id]
  }, exports)
  const node = type => ({ type, props: {}, children: [], parent: null })
  const renderer = vue.createRenderer({
    createElement: node, createText: () => node('text'), createComment: () => node('comment'),
    setText() {}, setElementText() {},
    patchProp(el, key, previous, value) { el.props[key] = value },
    insert(el, parent, anchor) { el.parent = parent; const i = parent.children.indexOf(anchor); parent.children.splice(i < 0 ? parent.children.length : i, 0, el) },
    remove(el) { el.parent.children.splice(el.parent.children.indexOf(el), 1) },
    parentNode: el => el.parent,
    nextSibling: el => el.parent?.children[el.parent.children.indexOf(el) + 1],
  })
  const root = node('root')
  const app = renderer.createApp(exports.default, { selected: null, currentVersion: '2.0.14' })
  app.mount(root)
  const all = el => [el, ...el.children.flatMap(all)]
  return { app, nodes: () => all(root) }
}

for (const name of ['GroupSidebar', 'AppUpdatePanel']) {
  test(`${name}: desktop has one native opener and ignores clicks while pending`, async () => {
    let calls = 0, finish
    const { app, nodes } = mount(name, true, () => { calls++; return new Promise(resolve => { finish = resolve }) })
    const button = nodes().find(n => n.type === 'button' && /github-link|release-link/.test(n.props.class))
    assert.ok(button)
    assert.equal(button.props.href, undefined)
    assert.equal(button.props.target, undefined)
    assert.equal(nodes().some(n => n.type === 'a' && n.props.href?.includes('github.com')), false)
    const click = () => button.props.onClick({ stopPropagation() {} })
    const first = click()
    await click()
    await vue.nextTick()
    assert.equal(calls, 1)
    assert.equal(button.props.disabled, true)
    finish()
    await first
    await vue.nextTick()
    assert.equal(button.props.disabled, false)
    const second = click()
    assert.equal(calls, 2, 'later deliberate clicks remain usable')
    finish()
    await second
    app.unmount()
  })

  test(`${name}: Web uses a normal link with no native click handler`, () => {
    const { app, nodes } = mount(name, false, () => assert.fail('Web must not invoke the native opener'))
    const links = nodes().filter(n => n.type === 'a' && n.props.href?.includes('github.com/oooing/rundock'))
    assert.equal(links.length, 1)
    assert.equal(links[0].props.target, '_blank')
    assert.equal(links[0].props.onClick, undefined)
    app.unmount()
  })
}
