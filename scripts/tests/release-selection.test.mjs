import assert from 'node:assert/strict'
import test from 'node:test'
import { computed, ref } from 'vue'

import { loadMembers } from './helpers/release-members.mjs'
import { fixture as model } from './helpers/release-model.mjs'
const load=(names,deps)=>loadMembers(names,deps)

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
  const f=model({single:true})
  const fresh={...f.preflight.value,profile:{buildMode:'local',versionMode:'manual',createTag:false},suggestedVersion:'2.0.16'}
  const platform=f.productPlatforms.value[0]
  return {state:f,actions:f,platform,finish:async()=>f.applyPreflight(fresh,true),close:f.close}
}

test('late initial preflight preserves explicit platform choices and updates version data',async()=>{
  for(const selected of [true,false]) {
    const f=delayedPreflightFixture()
    try {
      f.actions.togglePlatform(f.platform,selected)
      await f.finish()
      assert.equal(f.state.buildMode.value,'github')
      assert.equal(f.state.targetChoices.value.desktop.selected,selected)
      assert.equal(f.state.createTag.value,true,'formal release retains a tag')
      assert.equal(f.state.preflight.value.suggestedVersion,'2.0.16')
    } finally {f.close()}
  }
})

test('late preflight preserves code-only intent and phase choices',async()=>{
  for(const action of ['code-only','phase']) {
    const f=delayedPreflightFixture()
    try {
      f.actions.togglePlatform(f.platform,true)
      if(action==='code-only')f.actions.toggleGitOnly(true)
      else f.actions.setTargetPhase('desktop','publish',false)
      await f.finish()
      assert.equal(f.state.buildMode.value,'github')
      assert.equal(f.state.gitOnly.value,action==='code-only')
      if(action==='code-only')assert.deepEqual(f.state.candidateRequest.value.selectedTargets,[])
      else assert.equal(f.state.targetChoices.value.desktop.publish,false)
    } finally {f.close()}
  }
})

test('untouched release options initialize from saved preferences without choosing an incompatible target',async()=>{
  const f=delayedPreflightFixture()
  try {
    await f.finish()
    assert.equal(f.state.buildMode.value,'local')
    assert.equal(f.state.createTag.value,true)
    assert.equal(f.state.versionMode.value,'manual')
    assert.equal(f.state.targetChoices.value.desktop.selected,false)
  } finally {f.close()}
})

test('a sole usable build platform defaults on without overriding a manual deselection',()=>{
  const target={id:'web'}, platform={targets:[target]}, choices=ref({web:{selected:false}}),editedReleaseOptions=new Set()
  const productPlatforms=ref([platform]),gitOnly=ref(false)
  const releaseIntent=ref('formal')
  const {selectSingleBuildPlatform}=load(['selectSingleBuildPlatform'],{releaseIntent,productPlatforms,gitOnly,editedReleaseOptions,targetChoices:choices,platformRunnableTargets:p=>p.targets})
  selectSingleBuildPlatform();assert.equal(choices.value.web.selected,true)
  choices.value.web.selected=false;editedReleaseOptions.add('targets')
  selectSingleBuildPlatform();assert.equal(choices.value.web.selected,false)
  editedReleaseOptions.clear();gitOnly.value=true
  selectSingleBuildPlatform();assert.equal(choices.value.web.selected,false)
  gitOnly.value=false;productPlatforms.value=[platform,{targets:[{id:'mobile'}]}]
  selectSingleBuildPlatform();assert.equal(choices.value.web.selected,false)
  productPlatforms.value=[{targets:[]}]
  selectSingleBuildPlatform();assert.equal(choices.value.web.selected,false)
})
