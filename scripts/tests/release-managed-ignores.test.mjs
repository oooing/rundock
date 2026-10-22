import assert from 'node:assert/strict'
import test from 'node:test'
import { fixture } from './helpers/release-model.mjs'

const files = [
  {path:'src/main.ts',category:'recommend',contentFingerprint:'main',selectedDefault:true},
  {path:'notes.txt',category:'review',contentFingerprint:'notes'},
]
const policy = {file:'.gitignore',revision:'original',managed:true,groups:[{id:'demos',patterns:['/demos/'],enabled:true}]}

test('restoring ignores retains unrelated selections and does not publish or auto-select demos', async () => {
  let model; const calls=[]
  model=fixture({api:{updateReleaseIgnores:async (...args)=>{
    calls.push(args)
    return {policy:{...policy,revision:'new'},preflight:{...model.preflight.value,managedIgnores:{...policy,revision:'new'},
      classifications:[...files,{path:'demos/new.ts',category:'local',contentFingerprint:'demo',selectedDefault:false}],
      changes:[...model.preflight.value.changes,{path:'demos/new.ts',status:'??',tracked:false}]}}
  }}})
  try {
    model.applyPreflight({...model.preflight.value,managedIgnores:policy,classifications:files})
    model.chooseSafetyFile(files[0],false)
    model.chooseSafetyFile(files[1],true)
    const version={...model.versionInputs.value}; const notes=model.releaseNotes.value
    await model.updateManagedIgnores('demos',false)
    assert.deepEqual(calls,[['fixture','demos',false,'original']])
    assert.equal(model.selected.value['src/main.ts'],false)
    assert.equal(model.selected.value['notes.txt'],true)
    assert.equal(!!model.selected.value['demos/new.ts'],false)
    assert.equal(model.manualDecisions.value.length,2)
    assert.deepEqual(model.versionInputs.value,version)
    assert.equal(model.releaseNotes.value,notes)
    assert.equal(model.ignoringFile.value,false)
    assert.equal(model.requests.length,0,'must not submit a release')
  } finally { model.close() }
})

test('ignore failures preserve current choices and release the busy guard', async () => {
  const model=fixture({api:{updateReleaseIgnores:async ()=>{throw Error('stale rules')}}})
  try {
    model.applyPreflight({...model.preflight.value,managedIgnores:policy,classifications:files})
    model.chooseSafetyFile(files[0],false)
    const selected={...model.selected.value}
    await assert.rejects(model.updateManagedIgnores('demos',false),/stale rules/)
    assert.deepEqual(model.selected.value,selected)
    assert.equal(model.ignoringFile.value,false)
    model.publishing.value=true
    await assert.rejects(model.updateManagedIgnores('demos',false),/当前不能修改/)
  } finally {model.close()}
})
