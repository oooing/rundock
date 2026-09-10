import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { computed, ref } from 'vue'
import { parse } from '@vue/compiler-sfc'

const source = parse(readFileSync(new URL('../../src/components/ReleaseModal.vue', import.meta.url), 'utf8')).descriptor.scriptSetup.content
const ast = ts.createSourceFile('ReleaseModal.ts', source, ts.ScriptTarget.Latest, true)
function load(names, dependencies) {
  const statements = ast.statements.filter(statement => names.includes(statement.name?.text)
    || (ts.isVariableStatement(statement) && statement.declarationList.declarations.some(d => names.includes(d.name.getText(ast)))))
  assert.equal(statements.length, names.length)
  const compiled = ts.transpileModule(statements.map(s => s.getText(ast)).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  return new Function(...Object.keys(dependencies), `${compiled}\nreturn { ${names.join(', ')} }`)(...Object.values(dependencies))
}

test('no target is distinct from code-only, including the notes regeneration signature', () => {
  const gitOnly = ref(false), selectedTargets = ref([])
  const s = load(['targetSelectionMissing', 'releaseNotesOptionsSignature'], {
    computed, gitOnly, selectedTargets, selectedPaths: ref(['file.vue']), createTag: ref(true), preflight: ref({ statusFingerprint: 'test' }),
    plannedVersions: ref([{ versionGroupId: 'repository', tagName: 'v2.0.15' }]),
  })
  assert.equal(s.targetSelectionMissing.value, true)
  const emptySignature = s.releaseNotesOptionsSignature.value
  gitOnly.value = true
  assert.equal(s.targetSelectionMissing.value, false)
  assert.notEqual(s.releaseNotesOptionsSignature.value, emptySignature)
  gitOnly.value = false
  selectedTargets.value = [{ targetId: 'windows', build: true }]
  assert.equal(s.targetSelectionMissing.value, false)
})

test('no selection produces neither a code-only summary nor a notes request', async () => {
  // Any access beyond the early guards would throw: no API or timer is needed
  // until the user has actually chosen a release target.
  const s = load(['summaryLines', 'scheduleReleaseNotesDraft', 'generateReleaseNotesDraft'], {
    computed, targetSelectionMissing: ref(true),
  })
  assert.deepEqual(s.summaryLines.value, [])
  s.scheduleReleaseNotesDraft()
  await s.generateReleaseNotesDraft(true)
})

test('the recovery action returns to Publish and scrolls/focuses target selection', async () => {
  const calls = []
  const s = load(['chooseReleaseTarget'], {
    switchReleaseTab: async tab => { calls.push(['tab', tab]) },
    platformSectionRef: ref({
      scrollIntoView: options => calls.push(['scroll', options]),
      focus: options => calls.push(['focus', options]),
    }),
  })
  await s.chooseReleaseTarget()
  assert.deepEqual(calls, [['tab', 'publish'], ['scroll', { block: 'start' }], ['focus', { preventScroll: true }]])
})
