import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { reactive } from 'vue'

// Execute the production store with only the native IPC boundary replaced.
const source = readFileSync(new URL('../../src/stores/appUpdate.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source.replace(/^import .*\n/gm, ''), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function store(invoke) {
  const exports = {}
  class Channel { onmessage = () => {} }
  new Function('exports', 'reactive', 'invoke', 'Channel', compiled)(exports, reactive, invoke, Channel)
  return exports
}
const info = { version: '2.0.14', notes: '', url: '', size: 100 }
test('startup checks and downloads once, but never installs automatically', async () => {
  const calls = []
  const s = store(async (cmd, args) => {
    calls.push(cmd)
    if (cmd === 'check_app_update') return info
    if (cmd === 'download_app_update') args.onProgress.onmessage({downloaded:100,total:100})
  })
  await Promise.all([s.prepareStartupUpdate(), s.prepareStartupUpdate()])
  assert.deepEqual(calls, ['check_app_update', 'download_app_update'])
  assert.equal(s.appUpdate.phase, 'ready')
  assert.equal(s.appUpdate.downloaded, 100)
  assert.equal(s.appUpdate.dialogOpen, true)
  await s.installAppUpdate()
  assert.equal(calls.at(-1), 'install_app_update')
})
test('no update and network failure do not download or install', async () => {
  for (const failure of [false, true]) {
    const calls = []
    const s = store(async cmd => { calls.push(cmd); if (failure) throw 'offline'; return null })
    await s.prepareStartupUpdate()
    assert.deepEqual(calls, ['check_app_update'])
    assert.equal(s.appUpdate.phase, failure ? 'idle' : 'current')
    assert.equal(s.appUpdate.error, failure ? 'offline' : '')
    assert.equal(s.appUpdate.dialogOpen, false)
  }
})

test('dismissed update finishes quietly and can reopen without losing the verified installer', async () => {
  let finish
  const calls = []
  const s = store(async cmd => {
    calls.push(cmd)
    if (cmd === 'check_app_update') return info
    if (cmd === 'download_app_update') await new Promise(resolve => { finish = resolve })
  })
  const preparing = s.prepareStartupUpdate()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(s.appUpdate.phase, 'downloading')
  assert.equal(s.appUpdate.dialogOpen, true)
  s.dismissAppUpdate()
  finish()
  await preparing
  assert.equal(s.appUpdate.phase, 'ready')
  assert.equal(s.appUpdate.dialogOpen, false, 'download completion must not reopen a dismissed dialog')
  assert.equal(s.appUpdate.info.version, info.version)
  await s.checkAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true)
  assert.equal(s.appUpdate.phase, 'ready')
  assert.deepEqual(calls, ['check_app_update', 'download_app_update'])
  await s.installAppUpdate()
  s.dismissAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true, 'installation cannot be cancelled after handing off')
})

test('installation error keeps the update ready for a manual retry', async () => {
  const s = store(async cmd => { if (cmd === 'check_app_update') return info; if (cmd === 'install_app_update') throw 'busy' })
  await s.prepareStartupUpdate()
  await s.installAppUpdate()
  assert.equal(s.appUpdate.phase, 'ready')
  assert.equal(s.appUpdate.error, 'busy')
  s.dismissAppUpdate()
  s.openAppUpdate()
  assert.equal(s.appUpdate.dialogOpen, true)
})
test('download failure leaves a manual retry, not an automatic retry loop', async () => {
  let downloads = 0
  const s = store(async cmd => { if (cmd === 'check_app_update') return info; if (++downloads === 1) throw 'interrupted' })
  await s.prepareStartupUpdate()
  assert.equal(s.appUpdate.phase, 'available')
  await s.prepareStartupUpdate()
  assert.equal(downloads, 1)
  await s.downloadAppUpdate()
  assert.equal(s.appUpdate.phase, 'ready')
  assert.equal(s.appUpdate.error, '')
})
test('startup respects an ongoing manual operation or an already downloaded update', async () => {
  for (const phase of ['checking','downloading','ready','installing','available']) {
    const s = store(async () => { assert.fail('must not start a second operation') })
    s.appUpdate.phase = phase
    await s.prepareStartupUpdate()
    assert.equal(s.appUpdate.phase, phase)
  }
})
