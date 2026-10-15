import assert from 'node:assert/strict'
import test from 'node:test'
import { fixture as model } from './helpers/release-model.mjs'
const fixture=()=>{const f=model({single:true,local:true});f.changeSyncPolicy('local');return f}

test('the single code-only entry cannot carry a tag, version or build action',()=>{
 const f=fixture()
 try {
  f.toggleGitOnly(true)
  assert.equal(f.releaseIntent.value,'save-progress')
  assert.equal(f.pushRemote.value,false)
  assert.equal(f.targetChoices.value.desktop.selected,true,'keep the selection when returning to release')
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
  assert.equal(f.candidateRequest.value.selectedTargets[0].targetId,'desktop')
  f.createTag.value=false
  assert.equal(f.createTag.value,true,'release version always creates a tag')
  f.targetChoices.value.desktop.selected=true
  assert.equal(f.candidateRequest.value.selectedTargets[0].targetId,'desktop')
  assert.equal(f.candidateRequest.value.intent,'formal')
 } finally {f.close()}
})

test('checking locks the release purpose',()=>{
 const f=fixture()
 try {f.checkingCandidate.value=true;f.changeReleaseIntent('save-progress');assert.equal(f.releaseIntent.value,'formal')}
 finally {f.close()}
})
