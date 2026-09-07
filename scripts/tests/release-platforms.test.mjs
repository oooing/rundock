import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { transform } from 'esbuild'

// Exercise the component's actual grouping code without importing the API client.
const source = readFileSync(new URL('../../src/components/ReleaseModal.vue', import.meta.url), 'utf8')
const grouping = source.slice(source.indexOf('const standardPlatforms ='), source.indexOf('\nfunction phaseAllowed('))
assert.ok(grouping.includes('const productPlatforms ='))
const compiled = await transform(grouping, { loader: 'ts', format: 'esm' })
const evaluate = new Function('computed', 'configuredTargets', 'tr', `${compiled.code}\nreturn productPlatforms.value`)
const cards = targets => evaluate(fn => ({ get value() { return fn() } }), { value: targets }, text => text)
const target = (id, kind, name, runner = { type: 'git-push', os: ['any'] }) => ({ id, kind, name, runner })

test('combined Web/server and cloud PC each have one correctly named card', () => {
  const targets = [target('web-server', 'server', 'Web＋服务器后端'), target('desktop', 'desktop', 'PC 客户端'),
    target('android', 'android', 'Android · 云端')]
  const result = cards(targets)
  assert.equal(result.length, 3)
  assert.deepEqual(result.map(c => c.name).sort(), targets.map(t => t.name).sort())
  assert.ok(!result.some(c => ['web', 'pc', 'mac'].includes(c.id)))
  assert.equal(result.find(c => c.id === 'server').targets[0], targets[0])
  assert.equal(result.find(c => c.id === 'custom:desktop').targets[0], targets[1])
})

test('configured but mode-incompatible local targets are not hidden', () => {
  const local = target('extension-local', 'extension', '浏览器扩展 · 本地', { type: 'local', os: ['any'] })
  assert.equal(cards([local])[0].targets[0], local)
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
