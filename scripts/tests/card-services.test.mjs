import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../../src/utils/cardServices.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const exports = {}
new Function('exports', compiled)(exports)
const { cardServices, cardServiceDetails } = exports
const service = (port, role = 'unknown') => ({ id: `${port}`, port, role, url: `http://localhost:${port}`, health: 'healthy' })

test('required services remain visible before auxiliary ports regardless of role', () => {
  const app = { status: 'running', services: [{ ...service(5284, 'frontend'), statusScope: 'auxiliary' }, service(17655, 'backend'), service(17656, 'frontend')] }
  assert.deepEqual(cardServices(app).map(r => r.port), [17656, 17655, 5284])
  assert.equal(cardServices(app)[2].statusScope, 'auxiliary')
})

test('three current services never become five by merging historical and proxy ports', () => {
  const app = { id: 'app', status: 'running', services: [service(8081), service(18009, 'backend'), service(9100, 'frontend')],
    knownServices: [service(8009, 'backend'), service(9100)], portHints: [18009, 9100, 8081, 7890] }
  const before = JSON.stringify(app)
  assert.deepEqual(cardServices(app).map(r => [r.port, r.source]), [[9100, 'current'], [18009, 'current'], [8081, 'current']])
  const details = cardServiceDetails(app)
  assert.deepEqual(details.history.map(r => [r.port, r.source]), [[8009, 'history']])
  assert.deepEqual(details.configured.map(r => [r.port, r.source]), [[7890, 'configured']])
  assert.equal(details.configured[0].url, '', 'a configured port must not invent a reachable URL')
  assert.equal(JSON.stringify(app), before, 'display must not rewrite persisted data')
})

test('active states with no discovered services never fall back to history or hints', () => {
  for (const status of ['starting', 'running', 'degraded', 'stopping']) {
    for (const services of [[], undefined, null]) {
      const app = { status, services, knownServices: [service(8009)], portHints: [7890] }
      assert.deepEqual(cardServices(app), [])
      assert.equal(cardServiceDetails(app).history.length, 1)
      assert.equal(cardServiceDetails(app).configured.length, 1)
    }
  }
})

test('stopped and failed projects keep their addresses without a live status', () => {
  for (const status of ['failed', 'stopped', 'checking', 'unknown']) {
    const rows = cardServices({ status, services: [], knownServices: [service(5173, 'frontend'), service(8000, 'backend')] })
    assert.equal(rows.length, 2)
    assert.ok(rows.every(r => r.source === 'history'))
    // Also tolerate a legacy backend returning stale services as current.
    assert.equal(cardServices({ status, services: [service(5173)] })[0].source, 'history')
  }
})

test('configured hints remain accessible in details without inflating service count', () => {
  const app = { status: 'failed', portHints: [3000, 8000, 0, 99999] }
  assert.deepEqual(cardServices(app), [])
  assert.deepEqual(cardServiceDetails(app).configured.map(r => r.port), [3000, 8000])
})

test('deduplicates valid integer ports across visible services and detail groups', () => {
  const invalid = [0, -1, 65536, NaN, Infinity, 123.5, '7890', null]
  for (const status of ['running', 'stopped']) {
    const app = { status, services: [service(7890, 'frontend'), service(7890), ...invalid.map(p => service(p))],
      knownServices: [service(7890), service(8009), service(8009), ...invalid.map(p => service(p))],
      portHints: [7890, 8009, 1, 65535, 1, ...invalid] }
    const rows = cardServices(app), details = cardServiceDetails(app)
    assert.deepEqual(rows.map(r => r.port), status === 'running' ? [7890] : [7890, 8009])
    assert.deepEqual(details.history.map(r => r.port), status === 'running' ? [8009] : [])
    assert.deepEqual(details.configured.map(r => r.port), [1, 65535])
    assert.equal(rows[0].role, 'frontend')
  }
})
