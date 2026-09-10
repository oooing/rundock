import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as vue from 'vue'

function compile(path, modules = {}) {
  const source = readFileSync(new URL('../../' + path, import.meta.url), 'utf8')
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
  const exports = {}
  new Function('exports', 'require', js)(exports, id => { assert.ok(id in modules, id); return modules[id] })
  return exports
}
const colors = compile('src/utils/cardColors.ts')
function luminance(color) {
  const c = [1, 3, 5].map(i => parseInt(color.slice(i, i + 2), 16) / 255)
    .map(v => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4)
  return c[0] * .2126 + c[1] * .7152 + c[2] * .0722
}
function contrast(a, b) {
  const x = luminance(a), y = luminance(b)
  return (Math.max(x, y) + .05) / (Math.min(x, y) + .05)
}
test('custom colors stay opaque and readable with clear stopped-state dimming', () => {
  const palette = new Set(colors.CARD_COLOR_PALETTE)
  for (const r of [0, 51, 102, 153, 204, 255]) for (const g of [0, 51, 102, 153, 204, 255]) for (const b of [0, 51, 102, 153, 204, 255]) {
    palette.add('#' + [r, g, b].map(c => c.toString(16).padStart(2, '0')).join(''))
  }
  for (const original of palette) {
    for (const stopped of [false, true]) {
      const s = colors.getCardVisualStyle(original, stopped), bg = s['--card-bg']
      assert.match(bg, /^#[0-9a-f]{6}$/)
      for (const key of ['--card-fg', '--card-muted', '--card-link', '--card-status-green', '--card-running-text', '--card-status-amber', '--card-status-red']) {
        assert.ok(contrast(bg, s[key]) >= 4.5, `${original} stopped=${stopped} ${key}`)
      }
      assert.ok(contrast(bg, s['--card-glow']) >= 3, `edge visibility on ${original}`)
      assert.ok(contrast(s['--card-action-bg'], s['--card-action-fg']) >= 4.5, `button on ${original}`)
      if (stopped && luminance(original) > .01) assert.ok(luminance(bg) < luminance(original) * .6, `dimming ${original}`)
      if (!stopped) assert.equal(bg, original, 'running preserves the chosen color')
    }
  }
  assert.deepEqual(colors.getCardVisualStyle('invalid'), {})
})

test('motion selection restores across reloads, normalizes invalid values and tolerates unavailable storage', t => {
  const before = globalThis.window
  t.after(() => { globalThis.window = before })
  const data = new Map()
  globalThis.window = { localStorage: { getItem: k => data.get(k), setItem: (k,v) => data.set(k,v) } }
  const load = () => compile('src/stores/motion.ts', { vue })
  let motion = load()
  assert.equal(motion.runningEffect.value, 'breathe')
  for (const effect of ['orbit', 'chase', 'ripple', 'none', 'breathe']) {
    motion.setRunningEffect(effect)
    assert.equal(motion.runningEffect.value, effect)
    assert.equal(load().runningEffect.value, effect)
  }
  motion.setRunningEffect('unknown')
  assert.equal(load().runningEffect.value, 'breathe')
  globalThis.window = { localStorage: { getItem() { throw Error('denied') }, setItem() { throw Error('denied') } } }
  motion = load()
  motion.setRunningEffect('orbit')
  assert.equal(motion.runningEffect.value, 'orbit')
})
