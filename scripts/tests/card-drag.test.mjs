import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../../src/utils/cardDrag.ts', import.meta.url), 'utf8')
const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const helpers = {}
new Function('exports', js)(helpers)

test('drop side determines the resulting order in both drag directions', () => {
  const original = ['a', 'b', 'c', 'd']
  assert.deepEqual(helpers.reorderAtEdge(original, 'a', 'c', 'before'), ['b', 'a', 'c', 'd'])
  assert.deepEqual(helpers.reorderAtEdge(original, 'a', 'c', 'after'), ['b', 'c', 'a', 'd'])
  assert.deepEqual(helpers.reorderAtEdge(original, 'd', 'b', 'before'), ['a', 'd', 'b', 'c'])
  assert.deepEqual(helpers.reorderAtEdge(original, 'd', 'b', 'after'), ['a', 'b', 'd', 'c'])
  assert.deepEqual(helpers.reorderAtEdge(original, 'a', 'a', 'after'), original)
  assert.deepEqual(helpers.reorderAtEdge(original, 'missing', 'b', 'after'), original)
  assert.deepEqual(original, ['a', 'b', 'c', 'd'], 'calculating the preview must not mutate the saved order')
})

test('multi-column grids use left/right and single-column grids use top/bottom', () => {
  const rect = { left: 100, top: 200, width: 300, height: 180 }
  assert.equal(helpers.edgeAtPoint(rect, 120, 350, false), 'before')
  assert.equal(helpers.edgeAtPoint(rect, 380, 220, false), 'after')
  assert.equal(helpers.edgeAtPoint(rect, 380, 220, true), 'before')
  assert.equal(helpers.edgeAtPoint(rect, 120, 350, true), 'after')
})
