import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { build } from 'esbuild'
import { runInNewContext } from 'node:vm'

const root = new URL('../../', import.meta.url)
const dictionary = JSON.parse(readFileSync(new URL('src/i18n/en.json', root), 'utf8'))
const compiled = await build({ entryPoints: [fileURLToPath(new URL('src/i18n/productCopy.ts', root))], bundle: true, platform: 'node', format: 'cjs', write: false })
const context = { module: { exports: {} } }
runInNewContext(compiled.outputFiles[0].text, context)
const { labelCopy, detailCopy } = context.module.exports
const tokens = value => (value.match(/\{\d+\}/g) || []).sort().join(',')

test('edited copy preserves translation keys, English coverage and interpolation', () => {
  const problems = []
  for (const [key, [zh, en]] of Object.entries({ ...labelCopy, ...detailCopy })) {
    if (!Object.hasOwn(dictionary, key)) problems.push(`Unknown key: ${key}`)
    if (!zh.trim() || !en.trim() || /[\u3400-\u9fff]/.test(en)) problems.push(`Incomplete translation: ${key}`)
    if (tokens(key) !== tokens(zh) || tokens(key) !== tokens(en)) problems.push(`Changed placeholders: ${key}`)
  }
  assert.deepEqual(problems, [])
})

test('authored Chinese labels have 2–6 characters and descriptions at most 40', () => {
  const problems = []
  for (const [key, [zh]] of Object.entries(labelCopy)) {
    if ([...zh].length < 2 || [...zh].length > 6) problems.push(`Label (${[...zh].length}): ${key} → ${zh}`)
  }
  for (const [key, [zh]] of Object.entries(detailCopy)) {
    if ([...zh].length > 40) problems.push(`Description (${[...zh].length}): ${key} → ${zh}`)
    if (Object.hasOwn(labelCopy, key)) problems.push(`Duplicate category: ${key}`)
  }
  assert.deepEqual(problems, [])
})
