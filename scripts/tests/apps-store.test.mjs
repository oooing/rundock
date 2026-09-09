import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

// Run the actual store with controlled HTTP/WS ordering, without a browser framework.
function setup() {
  let handler
  let current = { id: 'app', status: 'stopped', runId: '', pid: 0, services: [], lastUrl: '' }
  let read = async () => structuredClone(current)
  const api = {
    getApp: () => read(), listApps: async () => [structuredClone(current)],
    start: async () => ({ ok: true, data: { configUpdated: false } }),
    restart: async () => ({ ok: true, data: { configUpdated: false } }),
    stop: async () => ({ stopped: true }),
  }
  const exports = {}
  const source = readFileSync(new URL('../../src/stores/apps.ts', import.meta.url), 'utf8')
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
  vm.runInNewContext(js, { exports, require: name => ({
    pinia: { defineStore: (_, setup) => setup }, vue: { ref: value => ({ value }) },
    '@/api/http': { api }, '@/api/ws': { wsClient: { on: fn => { handler = fn } } },
    '@/i18n': { tr: x => x }, '@/utils/cardColors': { pickNextCardColor: () => '' },
  })[name] })
  const store = exports.useAppsStore()
  store.apps.value = [structuredClone(current)]
  store.bindWS()
  return { store, emit: msg => handler({ appId: 'app', ...msg }),
    set: value => { current = { ...current, ...value } },
    read: fn => { read = fn }, view: () => store.apps.value[0] }
}
const tick = () => new Promise(resolve => setImmediate(resolve))

test('ordinary start refreshes PID, restart switches run, stop clears live services', async () => {
  const h = setup()
  h.set({ status: 'running', pid: 100, runId: 'first', lastUrl: 'http://localhost:4310/library', services: [{ appRunId: 'first', health: 'healthy' }] })
  await h.store.start('app')
  assert.equal(h.view().pid, 100)
  assert.equal(h.view().status, 'running') // Must not regress to starting after response.
  h.set({ status: 'starting', pid: 200, runId: 'second', services: [] })
  await h.store.restart('app')
  assert.equal(h.view().pid, 200)
  assert.equal(h.view().runId, 'second')
  assert.equal(h.view().services.length, 0)
  h.set({ status: 'stopped', pid: 0, runId: '', services: [] })
  await h.store.stop('app')
  assert.equal(h.view().pid, 0)
  assert.equal(h.view().services.length, 0)
})

test('late old-run events cannot revive services or overwrite the main URL', async () => {
  const h = setup()
  h.set({ lastUrl: 'http://localhost:4310/library' })
  h.emit({ type: 'app:services', runId: 'old', services: [{ health: 'healthy' }] })
  h.emit({ type: 'app:url', url: 'http://localhost:4311' })
  h.emit({ type: 'app:status', status: 'running', runId: 'old' })
  await tick()
  assert.equal(h.view().status, 'stopped')
  assert.equal(h.view().pid, 0)
  assert.equal(h.view().services.length, 0)
  assert.equal(h.view().lastUrl, 'http://localhost:4310/library')
})

test('a status change during an HTTP read discards that old snapshot', async () => {
  const h = setup()
  let release, reads = 0
  h.read(async () => {
    if (++reads === 1) return new Promise(resolve => { release = resolve })
    return { id: 'app', status: 'stopped', pid: 0, runId: '', services: [] }
  })
  h.emit({ type: 'app:status', status: 'running' })
  h.emit({ type: 'app:status', status: 'stopped' })
  release({ id: 'app', status: 'running', pid: 123, runId: 'old', services: [{ health: 'healthy' }] })
  await tick()
  assert.equal(reads, 2)
  assert.equal(h.view().status, 'stopped')
  assert.equal(h.view().pid, 0)
})

test('WS reconnect refreshes runtime without a page reload', async () => {
  const h = setup()
  h.set({ status: 'running', pid: 300, runId: 'third' })
  h.emit({ type: 'hello' })
  await tick()
  assert.equal(h.view().pid, 300)
})
