import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { ref } from 'vue'

function loadInstaller(file, name, deps) {
  const source = readFileSync(new URL(`../../src/components/release/${file}.ts`, import.meta.url), 'utf8')
    .replace(/^import .*;\r?\n/gm, '').replace(`export function ${name}`, `function ${name}`)
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  return new Function(...Object.keys(deps), `${code};return ${name}`)(...Object.values(deps))
}
function fixture(stage, commitSha = '', ready = true) {
  const calls = [], ctx = { props: { app: { id: 'fixture' } } }
  loadInstaller('state', 'installState', { ref })(ctx)
  Object.assign(ctx, { candidateReady: ref(ready), load: () => { calls.push('load') } })
  loadInstaller('progress', 'installProgress', { api: {}, tr: s => s, ApiError: Error, rememberReleaseSession: () => {} })(ctx)
  ctx.activeRun.value = { id: 'failed-run', status: 'failed', stage, commitSha }
  ctx.candidate.value = { id: 'accepted', accepted: true }
  ctx.candidateSignature.value = 'unchanged-plan'
  ctx.sensitiveExceptions.value = [{ fingerprint: 'test-password', reason: 'fixture' }]
  ctx.releaseNotes.value = 'User edited release notes'
  ctx.releaseNotesDirty.value = true
  return { ctx, calls }
}

test('return from a pre-build connection failure keeps the accepted content and user decisions', () => {
  for (const stage of ['delivery_connecting', 'delivery_preflight', 'preparing']) {
    const { ctx, calls } = fixture(stage)
    ctx.startNew()
    assert.equal(ctx.activeRun.value, null)
    assert.equal(ctx.candidate.value.id, 'accepted')
    assert.equal(ctx.candidateSignature.value, 'unchanged-plan')
    assert.equal(ctx.sensitiveExceptions.value.length, 1)
    assert.equal(ctx.releaseNotes.value, 'User edited release notes')
    assert.equal(ctx.releaseNotesDirty.value, true)
    assert.deepEqual(calls, [])
  }
})

test('changed candidates and already committed releases use the normal refresh path', () => {
  for (const args of [['delivery_connecting', '', false], ['delivery_preflight', 'committed'], ['target_build', '']]) {
    const { ctx, calls } = fixture(...args)
    ctx.startNew()
    assert.equal(ctx.candidate.value, null)
    assert.equal(ctx.candidateSignature.value, '')
    assert.deepEqual(calls, ['load'])
  }
})
