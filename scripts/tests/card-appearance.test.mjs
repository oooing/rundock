import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as vue from 'vue'

const source = readFileSync(new URL('../../src/stores/cardAppearance.ts', import.meta.url), 'utf8')
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
function load() {
  const exports = {}
  new Function('exports', 'require', js)(exports, id => { assert.equal(id, 'vue'); return vue })
  return exports
}
test('appearance defaults, independent choices and reload persistence', t => {
  const previous = globalThis.window
  t.after(() => { globalThis.window = previous })
  const data = new Map([['other-setting', 'untouched']])
  globalThis.window = { localStorage: { getItem: key => data.get(key), setItem: (key, value) => data.set(key, value) } }
  let state = load()
  assert.equal(state.cardDesign.value, 'icon')
  assert.equal(state.cardHandlePosition.value, 'top-center')
  assert.equal(data.size, 1, 'loading defaults does not write settings')
  state.setCardDesign('watermark')
  state.setCardHandlePosition('top-left')
  state = load()
  assert.equal(state.cardDesign.value, 'watermark')
  assert.equal(state.cardHandlePosition.value, 'top-left')
  state.setCardDesign('icon')
  assert.equal(load().cardHandlePosition.value, 'top-left', 'changing the icon leaves the handle preference intact')
  state.setCardHandlePosition('top-center')
  assert.equal(load().cardDesign.value, 'icon')
  assert.equal(data.get('other-setting'), 'untouched')
  state.setCardDesign('invalid'); state.setCardHandlePosition('removed-position')
  assert.equal(state.cardDesign.value, 'icon'); assert.equal(state.cardHandlePosition.value, 'top-center')
  for (const bad of ['{bad', 'null', '[]', '"text"', '{"design":"removed","handlePosition":4}']) {
    data.set(state.CARD_APPEARANCE_STORAGE_KEY, bad)
    assert.equal(load().cardDesign.value, 'icon'); assert.equal(load().cardHandlePosition.value, 'top-center')
  }
})
test('unavailable storage never blocks the UI and reports session-only settings', t => {
  const previous = globalThis.window
  t.after(() => { globalThis.window = previous })
  globalThis.window = { localStorage: { getItem() { throw Error('blocked') }, setItem() { throw Error('blocked') } } }
  const state = load()
  state.setCardDesign('watermark'); state.setCardHandlePosition('top-left')
  assert.equal(state.cardDesign.value, 'watermark'); assert.equal(state.cardHandlePosition.value, 'top-left')
  assert.equal(state.cardAppearanceSaveFailed.value, true)
  let written
  globalThis.window.localStorage.setItem = (key, value) => { written = JSON.parse(value) }
  state.setCardDesign('icon')
  assert.deepEqual(written, { design: 'icon', handlePosition: 'top-left' })
  assert.equal(state.cardAppearanceSaveFailed.value, false)
})
