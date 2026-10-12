import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { ref } from 'vue'

const source = readFileSync(new URL('../../src/components/release/preferences.ts', import.meta.url), 'utf8')
const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const tick = () => new Promise(resolve => setImmediate(resolve))
function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function fixture() {
  const requests = [], applied = [], resumed = []
  const api = Object.fromEntries(['releasePreflight', 'listReleases', 'getReleaseConfig'].map(method => [method, appId => {
    const request = { method, appId, ...deferred() }; requests.push(request); return request.promise
  }]))
  const exports = {}
  new Function('require', 'exports', code)(id => {
    if (id === '@/api/http') return { api, ApiError: Error }
    if (id === '@/i18n') return { tr: text => text }
    if (id === '@/utils/releasePreferences') return { readReleasePreferences: () => ({}) }
    throw Error(id)
  }, exports)
  const ctx = { props: { app: { id: 'fixture' } }, disposed: false, editedReleaseOptions: new Set(),
    applyReleaseConfig: config => applied.push(['config', config]), applyPreflight: pf => applied.push(['preflight', pf]),
    showRun: run => resumed.push(run), messageOf: error => error.message }
  for (const key of ['loading', 'profileReady', 'error', 'errorCode', 'versionPlanNotice', 'configNotice', 'history', 'configEndpointAvailable', 'gitOnly']) ctx[key] = ref(null)
  exports.installPreferences(ctx)
  return { ctx, requests, applied, resumed, settle(batch, pf = { version: '2.0.34' }) {
    requests[batch * 3].resolve(pf); requests[batch * 3 + 1].resolve([]); requests[batch * 3 + 2].resolve({ targets: [] })
  } }
}
test('the form stays unready until slow preflight has restored the complete snapshot', async () => {
  const f = fixture(), pending = f.ctx.load()
  f.requests[1].resolve([]); f.requests[2].resolve({ targets: ['windows'] }); await tick()
  assert.equal(f.ctx.loading.value, true)
  assert.equal(f.ctx.profileReady.value, false)
  f.requests[0].resolve({ version: '2.0.34' }); await pending
  assert.equal(f.ctx.loading.value, false); assert.equal(f.ctx.profileReady.value, true)
  assert.deepEqual(f.applied.map(item => item[0]), ['config', 'preflight'])
})
test('active runs resume without exposing a partial editable form', async () => {
  const f = fixture(), pending = f.ctx.load(), run = { id: 'active', status: 'running' }
  f.requests[1].resolve([run]); f.requests[2].resolve({ targets: [] }); await tick()
  assert.deepEqual(f.resumed, [run]); assert.equal(f.ctx.profileReady.value, false)
  f.requests[0].resolve({}); await pending
})
test('failed initialization stays unready and a retry can recover', async () => {
  const f = fixture(), first = f.ctx.load()
  f.requests[0].reject(Error('offline')); f.requests[1].resolve([]); f.requests[2].resolve({}); await first
  assert.equal(f.ctx.loading.value, false); assert.equal(f.ctx.profileReady.value, false)
  assert.equal(f.ctx.error.value, 'offline')
  const retry = f.ctx.load(); f.settle(1); await retry
  assert.equal(f.ctx.profileReady.value, true); assert.equal(f.ctx.error.value, '')
})
test('an older preflight cannot overwrite newer choices or end a newer loading state', async () => {
  const f = fixture(), first = f.ctx.load()
  f.requests[1].resolve([]); f.requests[2].resolve({}); await tick()
  const second = f.ctx.load()
  f.requests[0].resolve({ version: 'old' }); await first
  assert.equal(f.ctx.loading.value, true); assert.equal(f.ctx.profileReady.value, false)
  assert.equal(f.applied.filter(([kind]) => kind === 'preflight').length, 0)
  f.settle(1, { version: 'new' }); await second
  assert.deepEqual(f.applied.at(-1), ['preflight', { version: 'new' }])
})
test('late failures from a disposed dialog or superseded request do not alter state', async () => {
  for (const disposed of [true, false]) {
    const f = fixture(), first = f.ctx.load()
    f.requests[1].resolve([]); f.requests[2].resolve({}); await tick()
    const second = disposed ? null : f.ctx.load()
    f.ctx.disposed = disposed
    f.requests[0].reject(Error('late error')); await first
    assert.equal(f.ctx.error.value, ''); assert.equal(f.ctx.profileReady.value, false)
    assert.equal(f.ctx.loading.value, true)
    if (second) { f.settle(1); await second }
  }
})
