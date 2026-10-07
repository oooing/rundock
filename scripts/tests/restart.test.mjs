import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
const code = ts.transpileModule(readFileSync(new URL('../../src/utils/restart.ts', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const exports = {}
new Function('exports', code)(exports)
test('restart confirmation needs unexpired explicit permission', () => {
  const plan = { canRestart: true, confirmationToken: 'token', expiresAt: '2026-10-08T00:00:00Z' }
  const now = Date.parse('2026-10-07T00:00:00Z')
  assert.equal(exports.restartConfirmationValid(plan, now), true)
  for (const invalid of [null, {}, { ...plan, confirmationToken: '' }, { ...plan, canRestart: false }, { ...plan, expiresAt: 'bad' }]) assert.equal(exports.restartConfirmationValid(invalid, now), false)
  assert.equal(exports.restartConfirmationValid(plan, Date.parse(plan.expiresAt)), false)
})
test('old worker and unreachable backend cannot report restart success', () => {
  assert.equal(exports.isReplacementReady('old', { status: 'ok', apiVersion: '2', instanceId: 'new' }), true)
  for (const health of [{}, { status: 'ok', apiVersion: '2', instanceId: 'old' }, { status: 'closing', apiVersion: '2', instanceId: 'new' }, { status: 'ok', apiVersion: '1', instanceId: 'new' }]) assert.equal(exports.isReplacementReady('old', health), false)
})
test('runtime notices and action dialogs have translations for every literal label', () => {
  const dictionary = JSON.parse(readFileSync(new URL('../../src/i18n/en.json', import.meta.url), 'utf8'))
  for (const file of ['RestartDialog', 'PortResolutionDialog', 'AppStartupNotice']) {
    const source = readFileSync(new URL(`../../src/components/${file}.vue`, import.meta.url), 'utf8')
    for (const match of source.matchAll(/tr\('([^']+)'\)/g)) assert.ok(dictionary[match[1]], `${file}: ${match[1]}`)
  }
})
