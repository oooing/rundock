import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import * as vue from 'vue'
import { build } from 'esbuild'
import { parse } from '@vue/compiler-sfc'
import { parse as parseTemplate } from '@vue/compiler-dom'
import ts from 'typescript'

const root = fileURLToPath(new URL('../../', import.meta.url))
const dictionary = JSON.parse(readFileSync(path.join(root, 'src/i18n/en.json'), 'utf8'))
const compiled = await build({ entryPoints: [path.join(root, 'src/i18n/index.ts')], bundle: true, external: ['vue'], platform: 'node', format: 'cjs', write: false })
function loadLocale({ saved = null, language, languages = [], blocked = false } = {}) {
  const storage = new Map(saved === null ? [] : [['rundock.ui.locale', saved]])
  const context = { module: { exports: {} }, require(id) { assert.equal(id, 'vue'); return vue }, console, navigator: { language, languages },
    window: { localStorage: {
      getItem(key) { if (blocked) throw Error('Storage blocked'); return storage.get(key) ?? null },
      setItem(key, value) { if (blocked) throw Error('Storage blocked'); storage.set(key, value) },
    } } }
  runInNewContext(compiled.outputFiles[0].text, context)
  return { ...context.module.exports, storage }
}
const { translate, normalizeLocale, setLocale, locale, tr } = loadLocale({ blocked: true })

test('first launch maps Chinese locales to Chinese and all others to English', () => {
  for (const language of ['zh', 'zh-CN', 'zh-TW', 'zh-HK', 'zh-Hans', 'zh-Hant-TW', 'ZH_cn']) {
    assert.equal(normalizeLocale(language), 'zh-CN', language)
    assert.equal(loadLocale({ language }).tr('设置'), '设置', language)
  }
  for (const language of [null, undefined, '', 'en', 'en-US', 'en-GB', 'fr-FR', 'ja-JP', 'de-DE', 'zhinvalid']) {
    assert.equal(normalizeLocale(language), 'en', String(language))
    assert.equal(loadLocale({ language }).tr('设置'), 'Settings', String(language))
  }
  assert.equal(loadLocale({ languages: ['zh-CN', 'en-US'] }).locale.value, 'zh-CN')
  assert.equal(loadLocale({ language: 'fr-FR', languages: ['fr-FR', 'zh-CN'] }).locale.value, 'en')
})

test('desktop display language takes priority over the browser before rendering', async () => {
  for (const [language, native, expected] of [['en-US', 'zh-CN', 'zh-CN'], ['zh-CN', 'en', 'en'], ['zh-CN', 'ja-JP', 'en']]) {
    const app = loadLocale({ language })
    await app.initializeLocale(async () => native)
    assert.equal(app.locale.value, expected)
    assert.equal(app.storage.size, 0, 'automatic detection must not create a manual preference')
  }
})

test('explicit preferences survive restarts and override the system language', async () => {
  for (const saved of ['en', 'zh-CN']) {
    const app = loadLocale({ saved, language: saved === 'en' ? 'zh-CN' : 'en-US' })
    await app.initializeLocale(() => { assert.fail('saved preference must skip detection') })
    assert.equal(app.locale.value, saved)
    const choice = saved === 'en' ? 'zh-CN' : 'en'
    app.setLocale(choice)
    assert.equal(app.storage.get('rundock.ui.locale'), choice)
    assert.equal(loadLocale({ saved: choice, language: saved }).locale.value, choice)
  }
})

test('missing, invalid or inaccessible storage still follows the system', async () => {
  for (const options of [{}, { saved: '' }, { saved: 'invalid' }, { saved: 'null' }, { blocked: true }]) {
    const app = loadLocale({ ...options, language: 'en-US' })
    await app.initializeLocale(async () => 'zh-CN')
    assert.equal(app.locale.value, 'zh-CN')
  }
})

test('unavailable native detection falls back without preventing startup', async () => {
  for (const detect of [async () => null, async () => { throw Error('No native bridge') }]) {
    const app = loadLocale({ language: 'zh-TW' })
    await app.initializeLocale(detect)
    assert.equal(app.locale.value, 'zh-CN')
  }
})

test('a delayed language detection cannot undo a manual selection', async () => {
  const app = loadLocale({ language: 'zh-CN', blocked: true })
  let resolve
  const detection = app.initializeLocale(() => new Promise(done => { resolve = done }))
  app.setLocale('en')
  resolve('zh-CN')
  await detection
  assert.equal(app.locale.value, 'en')
})

test('switching works without storage and only changes UI labels', () => {
  setLocale('en')
  assert.equal(locale.value, 'en')
  assert.equal(tr('设置'), 'Settings')
  assert.equal(tr('已选文件：'), 'Selected files:')
  assert.equal(tr('RunDock 启动坞'), 'RunDock')
  assert.equal(tr('用户自己命名的项目'), '用户自己命名的项目')
  setLocale('zh-CN')
  assert.equal(tr('设置'), '设置')
})

test('interpolation preserves user text without recursive replacement', () => {
  const name = '我的项目 {1} <script> & $&'
  assert.equal(translate('en', 'unknown'), 'unknown')
  assert.equal(translate('en', '「{0}」已导入', [name]), `“${name}” imported`)
  assert.equal(translate('zh-CN', '「{0}」已导入', [name]), `「${name}」已导入`)
  assert.equal(translate('en', '最新 Tag：{0}'), 'Latest tag: {0}')
})

test('translations are nonempty and preserve the same placeholders', () => {
  const tokens = value => [...value.matchAll(/\{\d+\}/g)].map(x => x[0]).sort()
  for (const [key, value] of Object.entries(dictionary)) {
    assert.ok(value.trim(), key)
    assert.deepEqual(tokens(value), tokens(key), key)
    assert.equal(/[\u3400-\u9fff]/.test(value), false, `Untranslated English value: ${key}`)
  }
})

test('every literal UI translation key has an English entry', () => {
  const files = readdirSync(path.join(root, 'src/components')).filter(x => x.endsWith('.vue')).map(x => `src/components/${x}`)
    .concat(['src/App.vue', 'src/views/Dashboard.vue', 'src/api/base.ts', 'src/stores/apps.ts', 'src/main.ts'])
  let count = 0
  function checkScript(text, file) {
    const ast = ts.createSourceFile(file + '.ts', text, ts.ScriptTarget.Latest, true)
    function visit(node) {
      if (ts.isCallExpression(node) && node.expression.getText(ast) === 'tr' && node.arguments[0] && ts.isStringLiteral(node.arguments[0])) {
        const key = node.arguments[0].text
        assert.ok(Object.hasOwn(dictionary, key), `${file}: missing ${key}`)
        count++
      }
      ts.forEachChild(node, visit)
    }
    visit(ast)
  }
  for (const file of files) {
    const source = readFileSync(path.join(root, file), 'utf8')
    if (!file.endsWith('.vue')) { checkScript(source, file); continue }
    const sfc = parse(source).descriptor
    checkScript(sfc.scriptSetup.content, file)
    function visit(node) {
      if (node.type === 5) checkScript(node.content.content, file)
      // Language names stay recognizable even after selecting the wrong language.
      if (node.type === 2 && /[\u3400-\u9fff]/.test(node.content)) assert.equal(node.content.trim(), '简体中文', `${file}: untranslated text`)
      for (const attr of node.props || []) {
        if (attr.type === 7 && attr.exp) checkScript(attr.exp.content, file)
        if (attr.type === 6 && attr.value) assert.equal(/[\u3400-\u9fff]/.test(attr.value.content), false, `${file}: untranslated attribute`)
      }
      for (const child of node.children || []) visit(child)
    }
    visit(parseTemplate(sfc.template.content))
  }
  assert.ok(count > 500, `Unexpectedly few translated labels: ${count}`)
})
