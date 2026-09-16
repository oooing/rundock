import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import {computed, effectScope, ref, watch} from 'vue'
import {parse} from '@vue/compiler-sfc'

const source=parse(readFileSync(new URL('../../src/components/ReleaseModal.vue',import.meta.url),'utf8')).descriptor.scriptSetup.content
const ast=ts.createSourceFile('modal.ts',source,ts.ScriptTarget.Latest,true)
const names=['changeReleaseIntent','toggleGitOnly','selectSingleBuildPlatform','candidateRequest']
const code=ts.transpileModule(ast.statements.filter(s=>names.includes(s.name?.text)
  || ts.isVariableStatement(s) && s.declarationList.declarations.some(d=>names.includes(d.name.getText(ast)))
  || s.getText(ast).startsWith('watch([releaseIntent,gitOnly,createTag]'))
  .map(s=>s.getText(ast)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText

function fixture(){
 const releaseIntent=ref('formal'),gitOnly=ref(false),createTag=ref(true),pushRemote=ref(true)
 const targetChoices=ref({web:{selected:true,build:true}}),checkingCandidate=ref(false)
 const deps={computed,watch,releaseIntent,gitOnly,createTag,pushRemote,targetChoices,checkingCandidate,
  editedReleaseOptions:new Set(),versionMode:ref('auto'),setDefaultCommitMessage(){},productPlatforms:ref([{targets:[{id:'web'}]}]),platformRunnableTargets:p=>p.targets,
  preflight:ref({statusFingerprint:'original'}),selectedPaths:ref(['src/main.js']),manualDecisions:ref([]),sensitiveExceptions:ref([]),
  primaryTargetVersion:ref('2.0.20'),plannedVersions:ref([{versionGroupId:'product',targetVersion:'2.0.20'}]),buildMode:ref('github'),
  selectedTargets:computed(()=>gitOnly.value?[]:Object.entries(targetChoices.value).filter(([,t])=>t.selected).map(([targetId])=>({targetId,build:true}))),
 }
 const scope=effectScope()
 const actions=scope.run(()=>new Function(...Object.keys(deps),`${code};return {${names.join(',')}}`)(...Object.values(deps)))
 return {...deps,...actions,close:()=>scope.stop()}
}

test('the single code-only entry cannot carry a tag, version or build action',()=>{
 const f=fixture()
 try {
  f.toggleGitOnly(true)
  assert.equal(f.releaseIntent.value,'save-progress')
  assert.equal(f.pushRemote.value,false)
  assert.equal(f.targetChoices.value.web.selected,false)
  // Late saved settings cannot silently turn code-only back into a release.
  f.createTag.value=true;f.gitOnly.value=false
  const req=f.candidateRequest.value
  assert.equal(req.createTag,false);assert.equal(req.targetVersion,'')
  assert.deepEqual(req.versions,[]);assert.deepEqual(req.selectedTargets,[])
  assert.equal(req.buildMode,'none');assert.equal(req.pushRemote,false)
  f.pushRemote.value=true
  assert.equal(f.candidateRequest.value.pushRemote,true)
  assert.equal(f.candidateRequest.value.createTag,false)
 } finally {f.close()}
})

test('switching back to release restores version mode and selects its sole build target',()=>{
 const f=fixture()
 try {
  f.changeReleaseIntent('save-progress');f.changeReleaseIntent('formal')
  assert.equal(f.gitOnly.value,false);assert.equal(f.createTag.value,true)
  assert.equal(f.candidateRequest.value.selectedTargets[0].targetId,'web')
  f.createTag.value=false
  assert.equal(f.createTag.value,true,'release version always creates a tag')
  f.targetChoices.value.web.selected=true
  assert.equal(f.candidateRequest.value.selectedTargets[0].targetId,'web')
  assert.equal(f.candidateRequest.value.intent,'formal')
 } finally {f.close()}
})

test('checking locks the release purpose',()=>{
 const f=fixture()
 try {f.checkingCandidate.value=true;f.changeReleaseIntent('save-progress');assert.equal(f.releaseIntent.value,'formal')}
 finally {f.close()}
})
