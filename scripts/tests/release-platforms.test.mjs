import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { computed, ref } from 'vue'

// Exercise the component's actual grouping code without importing the API client.
function load(file, dependencies) {
  const source = readFileSync(new URL(`../../src/${file}.ts`, import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '')
  const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('exports', ...Object.keys(dependencies), code)(exports, ...Object.values(dependencies))
  return exports
}
const presentation = load('utils/releasePresentation', {})
const { installPlatforms } = load('components/release/platforms', { computed, tr: text => text, ...presentation })
const cards = targets => {
  const ctx = { configuredTargets: ref(targets), releaseConfig: ref(null) }
  installPlatforms(ctx)
  return ctx.productPlatforms.value
}
const target = (id, kind, name, runner = { type: 'git-push', os: ['any'] }) => ({ id, kind, name, runner })

test('combined Web/server and cloud PC each have one correctly named card', () => {
  const targets = [target('web-server', 'server', 'Web＋服务器后端'), target('desktop', 'desktop', 'PC 客户端'),
    target('android', 'android', 'Android · 云端')]
  const result = cards(targets)
  assert.equal(result.length, 3)
  assert.deepEqual(result.map(c => c.name).sort(), targets.map(t => presentation.releaseTargetLabel(t.name)).sort())
  assert.ok(!result.some(c => ['web', 'pc', 'mac'].includes(c.id)))
  assert.equal(result.find(c => c.id === 'server').targets[0].id, targets[0].id)
  assert.equal(result.find(c => c.id === 'custom:desktop').targets[0].id, targets[1].id)
})

test('configured but mode-incompatible local targets are not hidden', () => {
  const local = target('extension-local', 'extension', '浏览器扩展 · 本地', { type: 'local', os: ['any'] })
  assert.equal(cards([local])[0].targets[0].id, local.id)
  assert.deepEqual(cards([]), [])
})

test('real standalone Web, Windows and Mac targets remain available', () => {
  const result = cards([target('web', 'web', '网站'),
    target('windows', 'desktop', 'Windows', { os: ['windows'] }),
    target('mac', 'desktop', 'macOS', { os: ['darwin'] })])
  assert.deepEqual(result.map(c => c.id), ['web', 'pc', 'mac'])
})

test('multiple targets on one platform keep the platform label and every target', () => {
  const result = cards([target('a', 'web', '站点 A'), target('b', 'web', '站点 B')])
  assert.equal(result.length, 1)
  assert.equal(result[0].name, 'Web 前端')
  assert.equal(result[0].targets.length, 2)
})
