import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

// Exercise the real modal loader with an isolated API. No build or retry endpoint exists.
function fixture(history, { saved, historyError } = {}) {
  const source = readFileSync(new URL('../../src/components/release/preferences.ts', import.meta.url), 'utf8')
    .replace(/^import .*\r?\n/gm, '')
  const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const shown = [], calls = []
  const deps = {
    api: {
      async releasePreflight() { calls.push('preflight'); return {} },
      async listReleases() { calls.push('history'); if (historyError) throw historyError; return history },
      async getReleaseConfig() { calls.push('config'); return {} },
    },
    tr: text => text,
    readReleasePreferences: () => ({}),
    readReleaseSession: () => saved,
  }
  const ctx = {
    props: { app: { id: 'fixture' } }, disposed: false, editedReleaseOptions: new Set(),
    applyReleaseConfig() {}, applyPreflight() {}, messageOf: String,
    showRun: run => shown.push(run),
  }
  for (const key of ['loading', 'error', 'errorCode', 'versionPlanNotice', 'configNotice', 'history', 'configEndpointAvailable', 'gitOnly', 'profileReady']) {
    ctx[key] = { value: null }
  }
  const exports = {}
  new Function('exports', ...Object.keys(deps), code)(exports, ...Object.values(deps))
  exports.installPreferences(ctx)
  return { ctx, shown, calls }
}
const old = { id: 'old-failure', appId: 'fixture', status: 'failed', createdAt: '2026-10-05 09:11:08' }
const latest = { id: 'latest-success', appId: 'fixture', status: 'succeeded', createdAt: '2026-10-05 12:31:30' }

for (const saved of [undefined, { appId: 'fixture' }, { appId: 'fixture', runId: old.id }, { appId: 'fixture', runId: latest.id }, { appId: 'fixture', submittedAt: 1 }]) {
  test(`new publish opens the form despite completed history and stale session ${JSON.stringify(saved)}`, async () => {
    const f = fixture([latest, old], { saved })
    await f.ctx.load()
    assert.deepEqual(f.shown, [])
    assert.equal(f.ctx.error.value, '')
    assert.deepEqual(f.calls.sort(), ['config', 'history', 'preflight'])
  })
}

for (const status of ['queued', 'running']) {
  test(`${status} task wins over cached result; opening new form cannot hide active work`, async () => {
    const active = { ...latest, id: 'active', status }
    const f = fixture([latest, active, old], { saved: { appId: 'fixture', runId: old.id } })
    await f.ctx.load()
    await f.ctx.load()
    assert.deepEqual(f.shown, [active, active])
  })
}

test('latest failure or cancellation stays in history instead of replacing the new form', async () => {
  for (const errorCode of ['release_commit_changed', 'release_cancelled']) {
    const failed = { ...latest, status: 'failed', errorCode }
    const f = fixture([failed, old])
    await f.ctx.load()
    assert.deepEqual(f.shown, [])
    assert.deepEqual(f.ctx.history.value, [failed, old])
  }
})

test('explicit new release opens the form without restoring any completed task', async () => {
  const f = fixture([latest, old], { saved: { appId: 'fixture', runId: old.id } })
  await f.ctx.load()
  assert.deepEqual(f.shown, [])
  assert.deepEqual(f.ctx.history.value, [latest, old], 'old runs remain in manual history')
})

test('empty or unavailable history never fabricates a result from a cached id', async () => {
  for (const historyError of [undefined, new Error('offline')]) {
    const f = fixture([], { saved: { appId: 'fixture', runId: old.id }, historyError })
    await f.ctx.load()
    assert.deepEqual(f.shown, [])
  }
})
