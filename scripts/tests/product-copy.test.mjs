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
const scoped = await build({ entryPoints: [fileURLToPath(new URL('src/i18n/releaseSettingsCopy.ts', root))], bundle: true, platform: 'node', format: 'cjs', write: false })
const scopeContext = { module: { exports: {} } }
runInNewContext(scoped.outputFiles[0].text, scopeContext)
const { releaseSettingsLabels, releaseSettingsDetails } = scopeContext.module.exports
const tokens = value => (value.match(/\{\d+\}/g) || []).sort().join(',')

test('an unavailable check is not presented as a failed code test', () => {
  assert.equal(releaseSettingsLabels['无法开始检查'][0], '无法检查')
  assert.notEqual(releaseSettingsLabels['无法开始检查'][0], '检查失败')
})

test('edited copy preserves translation keys, English coverage and interpolation', () => {
  const problems = []
  for (const [key, [zh, en]] of Object.entries({ ...labelCopy, ...detailCopy, ...releaseSettingsDetails, ...releaseSettingsLabels })) {
    if (!Object.hasOwn(dictionary, key)) problems.push(`Unknown key: ${key}`)
    if (!zh.trim() || !en.trim() || /[\u3400-\u9fff]/.test(en)) problems.push(`Incomplete translation: ${key}`)
    if (tokens(key) !== tokens(zh) || tokens(key) !== tokens(en)) problems.push(`Changed placeholders: ${key}`)
  }
  assert.deepEqual(problems, [])
})

test('release and settings names use 2–4 characters and explanations fit 40, without changing meanings or data', () => {
  const problems = []
  for (const [key, [zh]] of Object.entries(releaseSettingsLabels)) {
    if ([...zh].length < 2 || [...zh].length > 4) problems.push(`Label (${[...zh].length}): ${key} → ${zh}`)
    if (Object.hasOwn(releaseSettingsDetails, key)) problems.push(`Duplicate category: ${key}`)
  }
  for (const [key, [zh]] of Object.entries(releaseSettingsDetails)) {
    if ([...zh].length > 40) problems.push(`Description (${[...zh].length}): ${key} → ${zh}`)
  }
  assert.deepEqual(problems, [])
  assert.match(releaseSettingsDetails['项目正在运行；发布不会自动停止或重启。'][0], /继续运行/)
  assert.match(releaseSettingsDetails['两种方式都只保存在本机，不上传。'][0], /不会上传/)
  assert.match(releaseSettingsDetails['当前：关闭窗口时自动最小化到托盘（已记住）。'][0], /继续在后台运行/)
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
