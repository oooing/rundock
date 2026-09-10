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

function delayedPreflightFixture() {
  const target = {id:'windows', steps:{build:'build',package:'package',publish:'tag-push',deploy:''}}
  const state = {buildMode:ref('github'), pushRemote:ref(true), createTag:ref(true), versionMode:ref('auto'), gitOnly:ref(false),
    targetChoices:ref({windows:{selected:false,build:false,package:false,publish:true,deploy:false}}),
    preflight:ref(null),remoteName:ref('origin'),versionStrategy:ref('auto'),preReleaseCommand:ref(''),preflightStale:ref(false)}
  const actions=load(['editedReleaseOptions','applyPreflight','defaultTargetChoice','togglePlatform','toggleGitOnly','setTargetPhase'],{
    ...state, configuredTargets:ref([target]),normalizePreflight:value=>value,
    platformRunnableTargets:()=>[target],phaseOptions:ref(['build','package','publish','deploy'].map(key=>({key}))),
    phaseAllowed:phase=>state.buildMode.value==='github'?phase==='publish':['build','package'].includes(phase),
    readLocalPreferences:()=>({}), syncVersionInputs(){},setDefaultCommitMessage(){},resetSelection(){},scheduleReleaseNotesDraft(){},
  })
  let resolve
  const pending=new Promise(done=>{resolve=done}).then(pf=>actions.applyPreflight(pf,true))
  return {state,actions,finish:async()=>{resolve({profile:{buildMode:'local',versionMode:'manual',createTag:false},changes:[],suggestedVersion:'2.0.16'});await pending}}
}

test('late initial preflight preserves the platform selected while versions are loading',async()=>{
  const f=delayedPreflightFixture()
  f.actions.togglePlatform({},true)
  await f.finish()
  assert.equal(f.state.buildMode.value,'github','saved build mode must not invalidate the selected cloud target')
  assert.equal(f.state.targetChoices.value.windows.selected,true)
  assert.equal(f.state.targetChoices.value.windows.publish,true)
  assert.equal(f.state.gitOnly.value,false)
  assert.equal(f.state.createTag.value,false,'untouched tag preference still loads')
  assert.equal(f.state.preflight.value.suggestedVersion,'2.0.16','fresh version data still applies')
})

test('late preflight also preserves explicit deselection, code-only and phase choices',async()=>{
  for(const action of ['deselect','code-only','phase']){
    const f=delayedPreflightFixture()
    f.actions.togglePlatform({},true)
    if(action==='deselect')f.actions.togglePlatform({},false)
    if(action==='code-only')f.actions.toggleGitOnly(true)
    if(action==='phase')f.actions.setTargetPhase('windows','publish',false)
    await f.finish()
    assert.equal(f.state.buildMode.value,'github')
    assert.equal(f.state.gitOnly.value,action==='code-only')
    assert.equal(f.state.targetChoices.value.windows.selected,action==='phase')
    if(action==='phase')assert.equal(f.state.targetChoices.value.windows.publish,false)
  }
})

test('untouched release options still initialize from saved preferences',async()=>{
  const f=delayedPreflightFixture();await f.finish()
  assert.equal(f.state.buildMode.value,'local')
  assert.equal(f.state.createTag.value,false)
  assert.equal(f.state.versionMode.value,'manual')
  assert.equal(f.state.targetChoices.value.windows.selected,false)
})
