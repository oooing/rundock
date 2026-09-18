import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { parse } from '@vue/compiler-sfc'
import { ref, computed } from 'vue'
import ts from 'typescript'

// Exercise the actual modal decision handlers without publishing or running tools.
const source = parse(readFileSync('src/components/ReleaseModal.vue', 'utf8')).descriptor.scriptSetup.content
const ast = ts.createSourceFile('release.ts', source, ts.ScriptTarget.Latest, true)
const names = ['recordSensitiveException', 'chooseSafetyFile', 'continueResolvedReview']
const code = ts.transpileModule(ast.statements.filter(s => names.includes(s.name?.text)).map(s => s.getText(ast)).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
function fixture(sameFile = false) {
  const findings = ['one', 'two'].map((fingerprint, i) => ({ path: sameFile ? 'a' : ['a', 'b'][i], fingerprint, contentFingerprint: 'bytes' }))
  const selected = ref({ a: true, b: true }), manualDecisions = ref([]), sensitiveExceptions = ref([])
  const candidate = ref({ sensitiveFindings: findings, dependencyFindings: [] })
  const findingDecisions = ref({}), reviewSignature = ref(''), resolvingReview = ref(false), checkingCandidate = ref(false)
  const version = ref('1.0.1')
  const currentCandidateSignature = computed(() => JSON.stringify([selected.value, manualDecisions.value, sensitiveExceptions.value, version.value]))
  const originalSignature = currentCandidateSignature.value
  const reviewStale = computed(() => originalSignature !== currentCandidateSignature.value && reviewSignature.value !== currentCandidateSignature.value)
  const pendingFindings = computed(() => candidate.value.sensitiveFindings.filter(f => !findingDecisions.value[f.fingerprint]))
  let checks = 0
  const inspectCandidate = async () => { checks++; return true }
  const deps = { selected, manualDecisions, sensitiveExceptions, candidate, findingDecisions, reviewSignature, resolvingReview, checkingCandidate, currentCandidateSignature, reviewStale, pendingFindings, inspectCandidate }
  const handlers = new Function(...Object.keys(deps), `${code};return {${names.join(',')}}`)(...Object.values(deps))
  return { ...deps, ...handlers, findings, version, checks: () => checks }
}
test('two allowances are recorded independently and validated only after the last', () => {
  const f = fixture()
  f.recordSensitiveException(f.findings[0], 'test data')
  assert.equal(f.checks(), 0)
  assert.equal(f.findingDecisions.value.one, 'allow')
  assert.equal(f.reviewStale.value, false)
  f.recordSensitiveException(f.findings[0], 'duplicate click')
  assert.equal(f.sensitiveExceptions.value.length, 1)
  f.recordSensitiveException(f.findings[1], 'test data')
  assert.equal(f.checks(), 1)
  assert.equal(f.sensitiveExceptions.value.length, 2)
})
test('allow then exclude, or exclude then allow, validates the batch once', () => {
  for (const excludeFirst of [false, true]) {
    const f = fixture()
    const allow = () => f.recordSensitiveException(f.findings[0], 'test data')
    const exclude = () => f.chooseSafetyFile({ path: 'b', category: 'recommend', contentFingerprint: 'bytes' }, false)
    ;(excludeFirst ? exclude : allow)()
    assert.equal(f.checks(), 0)
    assert.equal(f.reviewStale.value, false)
    ;(excludeFirst ? allow : exclude)()
    assert.equal(f.checks(), 1)
    assert.equal(f.selected.value.b, false)
    assert.equal(f.findingDecisions.value.two, 'exclude')
  }
})
test('excluding a file handles every finding in that file, not one at a time', () => {
  const f = fixture(true)
  f.chooseSafetyFile({ path: 'a', category: 'recommend', contentFingerprint: 'bytes' }, false)
  assert.equal(f.pendingFindings.value.length, 0)
  assert.equal(f.checks(), 1)
})
test('excluding two different files waits for both decisions', () => {
  const f = fixture()
  f.chooseSafetyFile({ path: 'a', category: 'recommend', contentFingerprint: 'bytes' }, false)
  assert.equal(f.checks(), 0)
  assert.equal(f.pendingFindings.value.length, 1)
  f.chooseSafetyFile({ path: 'b', category: 'recommend', contentFingerprint: 'bytes' }, false)
  assert.equal(f.checks(), 1)
  assert.equal(f.pendingFindings.value.length, 0)
})
test('unrelated plan changes invalidate decisions and block another allowance', () => {
  const f = fixture()
  f.recordSensitiveException(f.findings[0], 'test data')
  f.version.value = '1.0.2'
  assert.equal(f.reviewStale.value, true)
  f.recordSensitiveException(f.findings[1], 'test data')
  assert.equal(f.checks(), 0)
  assert.equal(f.sensitiveExceptions.value.length, 1)
})
test('unknown findings, changed fingerprints, empty reasons and busy clicks do not resolve issues', () => {
  const f = fixture()
  f.recordSensitiveException({ ...f.findings[0], contentFingerprint: 'different' }, 'test data')
  f.recordSensitiveException({ ...f.findings[0], fingerprint: 'unknown' }, 'test data')
  f.recordSensitiveException(f.findings[0], ' ')
  f.checkingCandidate.value = true
  f.recordSensitiveException(f.findings[0], 'test data')
  assert.equal(f.sensitiveExceptions.value.length, 0)
  assert.equal(f.checks(), 0)
})
