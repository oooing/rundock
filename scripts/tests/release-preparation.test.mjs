import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import {ref} from 'vue'
class ApiError extends Error {constructor(message,code='',preflight=null,status=400){super(message);this.code=code;this.preflight=preflight;this.status=status}}
function moduleFor(file,api){
 const source=readFileSync(new URL('../../src/components/release/'+file+'.ts',import.meta.url),'utf8')
 const code=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 const exports={};new Function('exports','require',code)(exports,id=>id==='@/api/http'?{api,ApiError}:id==='vue'?{nextTick:async()=>{}}:{tr:x=>x,rememberReleaseSession:()=>{}});return exports
}
function fixture(){
 const rows=[],runs=[];let writes=0,fail=false
 const api={saveReleasePreparation:async(app,item)=>{writes++;if(fail)throw Error('offline');const result={...structuredClone(item),revision:item.revision+1};rows.splice(0,rows.length,result);return result},listReleasePreparations:async()=>structuredClone(rows),getReleaseRun:async id=>({run:{id}})}
 const ctx={props:{app:{id:'app'}},candidateRequest:ref({selectedPaths:['a'],selectedTargets:[],versions:[]}),commitMessage:ref('message'),releaseNotes:ref('notes'),syncPolicy:ref('local'),candidate:ref({id:'candidate'}),error:ref(''),versionPlanNotice:ref(''),preparation:ref(null),preparationHistory:ref([]),autoSubmitting:ref(false),publishing:ref(false),checkingCandidate:ref(false),submissionUncertain:ref(false),showRun:run=>runs.push(run.id),messageOf:String}
 moduleFor('preparation',api).installPreparation(ctx)
 return {ctx,rows,runs,api,get writes(){return writes},fail:()=>{fail=true}}
}
test('preparation writes keep ordered revisions and capture the choices at each phase',async()=>{
 const f=fixture(),a=f.ctx.savePreparation('checking');f.ctx.releaseNotes.value='later';const b=f.ctx.savePreparation('ready');await Promise.all([a,b]);
 assert.equal(f.ctx.preparation.value.revision,2);assert.equal(f.ctx.preparation.value.draft.releaseNotes,'later');assert.equal(f.rows.length,1)
 await f.ctx.savePreparation('submitted','run-1');const id=f.ctx.preparation.value.id;await f.ctx.savePreparation('checking');assert.notEqual(f.ctx.preparation.value.id,id);assert.equal(f.ctx.preparation.value.runId,'')
})
test('lost submit responses reconcile the exact preparation and never start another release',async()=>{
 const f=fixture();await f.ctx.savePreparation('submitting');const old=f.ctx.preparation.value;
 await f.ctx.restorePreparation(old);assert.equal(f.ctx.submissionUncertain.value,true);assert.deepEqual(f.runs,[])
 f.rows[0].runId='server-run';f.rows[0].status='submitted';await f.ctx.restorePreparation(old);
 assert.deepEqual(f.runs,['server-run']);assert.equal(f.ctx.submissionUncertain.value,false);assert.equal(f.writes,1)
})
test('a failed persistence write is explicit and preserves the last saved snapshot',async()=>{
 const f=fixture();await f.ctx.savePreparation('checking');const old=f.ctx.preparation.value;f.fail();await assert.rejects(f.ctx.savePreparation('ready'),/offline/);assert.equal(f.ctx.preparation.value,old)
})

test('publication transport loss locks resubmission and reconciles by candidate identity',async()=>{
 for(const outcome of ['accepted','unknown','rejected','server-error','before-request']) {
  const f=fixture();let submissions=0
  Object.assign(f.ctx,{candidateSignature:ref(''),reviewSignature:ref(''),findingDecisions:ref({})})
  Object.assign(f.ctx,{preflight:ref({statusFingerprint:'source'}),canPublish:ref(true),errorCode:ref(''),createTag:ref(true),primaryTargetVersion:ref('1.0.1'),plannedVersions:ref([]),versionMode:ref('manual'),gitOnly:ref(false),buildMode:ref('github'),pushRemote:ref(true),selectedTargets:ref([]),selectedPaths:ref(['a']),hasExternalAction:ref(true),profileBody:()=>({}),releaseErrorMessage:String,bodyRef:ref(null)})
  f.api.saveReleaseProfile=async()=>{if(outcome==='before-request')throw Error('offline')}
  f.api.createRelease=async()=>{submissions++;if(outcome==='accepted'){f.rows[0].status='submitted';f.rows[0].runId='server-run'};if(outcome==='rejected'||outcome==='server-error'){f.rows[0].revision++;f.rows[0].status='error';throw new ApiError('Check failed','candidate_stale',null,outcome==='server-error'?500:400)};throw TypeError('response lost')}
  moduleFor('submission',f.api).installSubmission(f.ctx)
  await f.ctx.publish()
  assert.equal(submissions,outcome==='before-request'?0:1)
  assert.equal(f.ctx.publishing.value,false)
  assert.equal(f.ctx.submissionUncertain.value,outcome==='unknown')
  assert.deepEqual(f.runs,outcome==='accepted'?['server-run']:[])
  if(outcome==='unknown') assert.match(f.ctx.error.value,/待确认/)
  if(outcome==='rejected') assert.equal(f.ctx.preparation.value.status,'error')
 }
})

test('late saves cannot replace a newer draft in the open form',async()=>{
 const f=fixture();let finish
 const save=f.api.saveReleasePreparation
 f.api.saveReleasePreparation=(...args)=>new Promise(resolve=>{finish=async()=>resolve(await save(...args))})
 const old=f.ctx.savePreparation('checking');await Promise.resolve();await Promise.resolve()
 const oldId=f.ctx.preparation.value.id
 f.ctx.preparation.value=null
 const next=f.ctx.savePreparation('checking');const newId=f.ctx.preparation.value.id
 await finish();await old
 assert.equal(f.ctx.preparation.value.id,newId);assert.notEqual(oldId,newId)
 await Promise.resolve();await finish();await next
 assert.equal(f.ctx.preparation.value.id,newId)
})
