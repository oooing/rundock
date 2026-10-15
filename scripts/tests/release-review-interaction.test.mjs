import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { computed, nextTick, ref } from 'vue'

// Exercise the real reactive model starting at the finding's confirmation
// handler, not just inspectCandidate(true). A lost reuse flag must fail here.
function installer(file, name, dependencies) {
  const source = readFileSync(new URL(`../../src/components/release/${file}.ts`, import.meta.url), 'utf8')
    .replace(/^import .*;\r?\n/gm, '').replace(`export function ${name}`, `function ${name}`)
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  return new Function(...Object.keys(dependencies), `${code};return ${name}`)(...Object.values(dependencies))
}
const installState = installer('state', 'installState', { ref })
const finding = { path: 'tests/password.py', fingerprint: 'finding-1', contentFingerprint: 'content-1', line: 63 }
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done }); return { promise, resolve } }
function fixture(findings = [finding]) {
  const calls = [], review = deferred(), checks = deferred()
  const api = new Proxy({
    reviewReleaseCandidate: async (...args) => { calls.push(['review', ...args.slice(0, 3)]); return review.promise },
    checkReleaseCandidate: async (...args) => { calls.push(['check', ...args]); return checks.promise },
  }, { get(target, key) { return target[key] || (() => { calls.push([key]); throw Error(`Unexpected call: ${String(key)}`) }) } })
  const ctx = { props: { app: { id: 'fixture' } } }
  installState(ctx)
  Object.assign(ctx, {
    savePreparation: async () => {},
    selectedPaths: ref(['tests/password.py']), primaryTargetVersion: ref('1.2.40'),
    plannedVersions: ref([{ versionGroupId: 'pc', targetVersion: '1.2.40' }]),
    selectedTargets: ref([{ targetId: 'desktop-local', build: true }]), messageOf: String,
  })
  ctx.preflight.value = { statusFingerprint: 'source-1' }
  installer('candidate', 'installCandidate', { api, tr: text => text, computed, nextTick })(ctx)
  ctx.candidate.value = { id: 'existing', status: 'blocked', accepted: false, sensitiveFindings: findings, dependencyFindings: [], checkResults: [] }
  ctx.candidateSignature.value = ctx.currentCandidateSignature.value
  const passed = { ...ctx.candidate.value, status: 'passed', accepted: true, sensitiveFindings: [] }
  return { ctx, calls, review, checks, passed }
}
async function settled(ctx) {
  for (let i = 0; i < 30 && ctx.resolvingReview.value; i++) await Promise.resolve()
  assert.equal(ctx.resolvingReview.value, false)
}

test('confirming one test password sends one review and preserves completed checks', async () => {
  const f = fixture()
  f.ctx.recordSensitiveException(finding, 'test fixture')
  f.ctx.recordSensitiveException(finding, 'test fixture')
  await Promise.resolve()
  assert.equal(f.ctx.checkingCandidate.value, true)
  assert.equal(f.calls.length, 1)
  assert.equal(f.calls[0][0], 'review')
  assert.equal(f.calls[0][2], 'existing')
  assert.equal(f.calls[0][3].length, 1)
  f.review.resolve(f.passed)
  await settled(f.ctx)
  assert.equal(f.ctx.candidateReady.value, true)
  assert.equal(f.ctx.candidate.value.id, 'existing')
  assert.deepEqual(f.calls.map(call => call[0]), ['review'])
})

test('after permission, only previously blocked project checks are continued once', async () => {
  const f = fixture()
  f.ctx.recordSensitiveException(finding, 'test fixture')
  f.review.resolve({ ...f.passed, accepted: false, status: 'pending' })
  for (let i = 0; i < 30 && f.calls.length < 2; i++) await Promise.resolve()
  assert.deepEqual(f.calls.map(call => call[0]), ['review', 'check'])
  assert.equal(f.calls[1][2], 'existing')
  f.checks.resolve(f.passed)
  await settled(f.ctx)
  assert.equal(f.ctx.candidateReady.value, true)
})

test('multiple findings wait for the last decision and changed selections reject stale confirmation', async () => {
  const second = { ...finding, fingerprint: 'finding-2', line: 70 }
  const f = fixture([finding, second])
  f.ctx.recordSensitiveException(finding, 'test fixture')
  assert.deepEqual(f.calls, [])
  assert.equal(f.ctx.pendingFindings.value.length, 1)
  f.ctx.recordSensitiveException(second, 'test fixture')
  await Promise.resolve()
  assert.equal(f.calls.length, 1)
  assert.equal(f.calls[0][3].length, 2)
  f.review.resolve(f.passed)
  await settled(f.ctx)

  const stale = fixture()
  stale.ctx.selectedPaths.value = ['different.py']
  stale.ctx.recordSensitiveException(finding, 'test fixture')
  assert.deepEqual(stale.calls, [])
  assert.equal(stale.ctx.sensitiveExceptions.value.length, 0)
})
