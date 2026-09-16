import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import {computed, ref} from 'vue'
import {parse} from '@vue/compiler-sfc'
const source=parse(readFileSync(new URL('../../src/components/ReleaseModal.vue',import.meta.url),'utf8')).descriptor.scriptSetup.content
const ast=ts.createSourceFile('modal.ts',source,ts.ScriptTarget.Latest,true)
const code=ts.transpileModule(ast.statements.filter(s=>['inspectCandidate','cancelCandidate','submitRelease'].includes(s.name?.text)).map(s=>s.getText(ast)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
function fixture(){
 const candidate=ref(null),checkingCandidate=ref(false),checks=[],polls=[],timers=[],applied=[],published=[]
 const prepared={id:'test',status:'pending',accepted:false,sensitiveFindings:[],dependencyFindings:[]}
 const canSubmit=ref(true),candidateReady=computed(()=>!checkingCandidate.value && (candidate.value?.accepted || candidate.value?.status==='ready' && candidate.value?.canSaveProgress))
 const deps={autoSubmitting:ref(false),canSubmit,candidateReady,canPublish:computed(()=>canSubmit.value && candidateReady.value),submissionPlanSignature:ref('original-plan'),publish:async()=>{published.push(candidate.value.id)},nextTick:async()=>{},bodyRef:ref(null),tr:v=>v,checkingCandidate,preflight:ref({}),error:ref(''),disposed:false,props:{app:{id:'app'}},candidate,candidateSignature:ref(''),candidateRequest:ref({intent:'formal'}),profileBody:()=>({}),applyPreflight:value=>applied.push(value),messageOf:String,
 api:{saveReleaseProfile:async()=>{},releasePreflight:async()=>({}),prepareReleaseCandidate:async()=>({...prepared}),checkReleaseCandidate:()=>{const d=deferred();checks.push(d);return d.promise},getReleaseCandidate:()=>{const d=deferred();polls.push(d);return d.promise},cancelReleaseCandidate:async()=>({...prepared,status:'cancelled'})},setTimeout:callback=>{timers.push(callback);return timers.length},clearTimeout:()=>{}}
 const actions=new Function(...Object.keys(deps),`let candidateEpoch=0,candidatePoll=null,candidateAbort=null;${code};return {inspectCandidate,cancelCandidate,submitRelease,dispose:()=>{disposed=true}}`)(...Object.values(deps))
 return {...actions,...deps,checks,polls,timers,applied,published}
}
async function until(test){for(let i=0;i<30&&!test();i++)await Promise.resolve();assert.ok(test(),'asynchronous stage not reached')}
test('late poll cannot overwrite completed candidate with a pending snapshot',async()=>{
 const f=fixture(),run=f.inspectCandidate();await until(()=>f.checks.length===1)
 const poll=f.timers[0]();await until(()=>f.polls.length===1)
 f.checks[0].resolve({id:'test',status:'passed',accepted:true});await run
 f.polls[0].resolve({id:'test',status:'pending',accepted:false});await poll
 assert.equal(f.candidate.value.status,'passed');assert.equal(f.candidate.value.accepted,true)
})
test('cancelled request rejection cannot stop or stale the next check',async()=>{
 const f=fixture(),old=f.inspectCandidate();await until(()=>f.checks.length===1)
 await f.cancelCandidate();const next=f.inspectCandidate();await until(()=>f.checks.length===2)
 f.checks[0].reject(new Error('old transport cancelled'));await old
 assert.equal(f.checkingCandidate.value,true);assert.equal(f.error.value,'')
 f.checks[1].resolve({id:'new',status:'passed',accepted:true});await next
 assert.equal(f.candidate.value.id,'new');assert.equal(f.checkingCandidate.value,false)
})
test('cancelled preflight response does not reset the new selection',async()=>{
 const f=fixture(),preflight=deferred(),applied=[]
 f.api.releasePreflight=()=>preflight.promise
 // applyPreflight was closed over; observe the mutable API dependency instead:
 // a stale preflight must never advance to candidate preparation.
 f.api.prepareReleaseCandidate=async()=>{applied.push('prepared');return {id:'x',sensitiveFindings:[],dependencyFindings:[]}}
 const old=f.inspectCandidate();await Promise.resolve();await f.cancelCandidate()
 preflight.resolve({statusFingerprint:'stale'});await old
 assert.deepEqual(applied,[]);assert.deepEqual(f.applied,[]);assert.equal(f.candidate.value,null)
})
test('second late cancel response cannot overwrite the next candidate',async()=>{
 const f=fixture(),old=f.inspectCandidate();await until(()=>f.checks.length===1)
 const cancels=[];f.api.cancelReleaseCandidate=()=>{const d=deferred();cancels.push(d);return d.promise}
 const a=f.cancelCandidate(),b=f.cancelCandidate();await until(()=>cancels.length===2)
 cancels[1].resolve({id:'old',status:'cancelled'});await b
 const next=f.inspectCandidate();await until(()=>f.checks.length===2)
 f.checks[1].resolve({id:'new',status:'passed',accepted:true});await next
 cancels[0].resolve({id:'old',status:'cancelled'});await a
 f.checks[0].resolve({id:'old',status:'passed'});await old
 assert.equal(f.candidate.value.id,'new');assert.equal(f.candidate.value.status,'passed')
})


test('one submit action awaits checks then submits exactly once',async()=>{
 const f=fixture(), run=f.submitRelease()
 await until(()=>f.checks.length===1)
 assert.deepEqual(f.published,[]); assert.equal(f.autoSubmitting.value,true)
 await f.submitRelease(); assert.equal(f.checks.length,1)
 f.checks[0].resolve({id:'verified',status:'passed',accepted:true}); await run
 assert.deepEqual(f.published,['verified']); assert.equal(f.autoSubmitting.value,false)
})

test('failed checks, cancellation, disposal, and changed plans never submit',async()=>{
 for(const scenario of ['failed','cancelled','disposed','plan-changed','transport-error']){
  const f=fixture(),run=f.submitRelease();await until(()=>f.checks.length===1)
  if(scenario==='cancelled') await f.cancelCandidate()
  if(scenario==='disposed') f.dispose()
  if(scenario==='plan-changed') f.submissionPlanSignature.value='changed-plan'
  if(scenario==='transport-error') f.checks[0].reject(new Error('network failed'))
  else f.checks[0].resolve({id:'result',status:scenario==='failed'?'failed':'passed',accepted:scenario!=='failed'})
  await run
  assert.deepEqual(f.published,[],scenario);assert.equal(f.autoSubmitting.value,false)
 }
})

test('code-only submit automatically scans but never runs formal build checks',async()=>{
 const f=fixture();f.candidateRequest.value.intent='save-progress'
 f.api.prepareReleaseCandidate=async()=>({id:'code-only',status:'ready',canSaveProgress:true,sensitiveFindings:[],dependencyFindings:[]})
 await f.submitRelease()
 assert.equal(f.checks.length,0);assert.deepEqual(f.published,['code-only'])
})
