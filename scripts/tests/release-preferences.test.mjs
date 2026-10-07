import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

function fixture() {
  const values = new Map()
  const storage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }
  const source = readFileSync(new URL('../../src/utils/releasePreferences.ts', import.meta.url), 'utf8')
  const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('exports', 'localStorage', code)(exports, storage)
  return { ...exports, values, storage }
}
test('preferences are isolated by project and merge independently saved build selections', () => {
  const f = fixture()
  f.writeReleasePreferences('one', { releaseIntent: 'save-progress', localVersionMode: 'upgrade', syncPolicy: 'local',
    buildMode: 'local', localSyncPolicy: 'local', versionMode: 'manual', checksEnabled: false, releaseTargets: { github: ['cloud'] } })
  f.writeReleasePreferences('one', { packagingTargets: ['android'], releaseTargets: { local: ['pc'] } })
  f.writeReleasePreferences('two', { packagingTargets: [] })
  const saved = f.readReleasePreferences('one')
  assert.equal(saved.releaseIntent, 'save-progress')
  assert.equal(saved.localVersionMode, 'upgrade')
  assert.equal(saved.checksEnabled, false)
  assert.equal(saved.localSyncPolicy, 'local')
  assert.deepEqual(saved.releaseTargets, { github: ['cloud'], local: ['pc'] })
  assert.deepEqual(saved.packagingTargets, ['android'])
  assert.deepEqual(f.readReleasePreferences('two'), { packagingTargets: [] })
})
test('empty selection is preserved; invalid/legacy state cannot inject approvals or versions', () => {
  const f = fixture()
  const key = f.releasePreferenceKey('one')
  for (const invalid of ['null', '[]', '{broken']) {
    f.values.set(key, invalid)
    assert.deepEqual(f.readReleasePreferences('one'), {})
  }
  f.values.set(key, JSON.stringify({ buildMode: 'local', localSyncPolicy: 'invalid', createTag: false, releaseIntent: 'bad',
    versionMode: 'unchanged', selectedPaths: ['secret.env'], candidateId: 'old-approval', versionInputs: { desktop: '9.0.0' },
    packagingTargets: [], releaseTargets: { local: [], github: ['pc', 'pc'], invalid: ['bad'] } }))
  assert.deepEqual(f.readReleasePreferences('one'), { buildMode: 'local', packagingTargets: [], releaseTargets: { github: ['pc'], local: [] } })
})
test('storage failures propagate for an honest unsaved status', () => {
  const f = fixture()
  f.storage.setItem = () => { throw new Error('quota') }
  assert.throws(() => f.writeReleasePreferences('one', { releaseIntent: 'formal' }), /quota/)
})

const statusSource = readFileSync(new URL('../../src/components/ReleasePreferenceStatus.vue', import.meta.url), 'utf8')
const statusTemplate = statusSource.match(/<template>([\s\S]*?)<\/template>/)[1]
for (const status of ['idle', 'saving', 'saved', 'error']) {
  test(`preference feedback stays quiet except on failure: ${status}`, async () => {
    const html = await renderToString(createSSRApp({ props: ['status'], template: statusTemplate,
      setup: () => ({ tr: text => text }) }, { status }))
    if (status === 'error') {
      assert.match(html, /选择未保存/)
      assert.match(html, /role="status"/)
    } else {
      assert.doesNotMatch(html, /<span|已记住选择|正在记住选择/)
    }
  })
}
