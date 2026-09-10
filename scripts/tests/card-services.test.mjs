import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../../src/utils/cardServices.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const exports = {}
new Function('exports', compiled)(exports)
const { cardServices } = exports
const service = (port, role = 'unknown') => ({ id: `${port}`, port, role, url: `http://localhost:${port}`, health: 'healthy' })

test('keeps all service ports, preferring the current run over historical duplicates', () => {
  const rows = cardServices({ id: 'app', status: 'running', services: [service(5173, 'frontend'), service(8000, 'backend')],
    knownServices: [service(5173), service(5432, 'database')], portHints: [8000, 9000, 9000] })
  assert.deepEqual(rows.map(r => [r.port, r.source]), [[5173, 'current'], [8000, 'current'], [5432, 'history'], [9000, 'configured']])
  assert.equal(rows[3].url, '', 'a configured port must not invent a reachable URL')
})

test('stopped and failed projects keep their addresses without a live status', () => {
  for (const status of ['failed', 'stopped']) {
    const rows = cardServices({ status, services: [], knownServices: [service(5173, 'frontend'), service(8000, 'backend')] })
    assert.equal(rows.length, 2)
    assert.ok(rows.every(r => r.source === 'history'))
    // Also tolerate a legacy backend returning stale services as current.
    assert.equal(cardServices({ status, services: [service(5173)] })[0].source, 'history')
  }
})

test('old backends still expose every configured port while waiting for discovery', () => {
  assert.deepEqual(cardServices({ status: 'failed', portHints: [3000, 8000, 0, 99999] }).map(r => r.port), [3000, 8000])
})
