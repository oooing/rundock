import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import test from 'node:test'
import { build } from 'esbuild'
import { parse } from '@vue/compiler-sfc'
import { parse as parseTemplate } from '@vue/compiler-dom'
import ts from 'typescript'

const root = fileURLToPath(new URL('../../', import.meta.url))
const dictionary = JSON.parse(readFileSync(path.join(root, 'src/i18n/en.json'), 'utf8'))
const compiled = await build({ entryPoints: [path.join(root, 'src/i18n/index.ts')], bundle: true, platform: 'node', format: 'esm', write: false })
const { translate, normalizeLocale, setLocale, locale, tr } = await import(`data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString('base64')}`)

test('Chinese is the default, including missing and unsupported preferences', () => {
  assert.equal(locale.value, 'zh-CN')
  for (const value of [null, undefined, '', 'fr', 'en-US', 'zh-CN']) assert.equal(normalizeLocale(value), 'zh-CN')
  assert.equal(normalizeLocale('en'), 'en')
  assert.equal(tr('设置'), '设置')
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
