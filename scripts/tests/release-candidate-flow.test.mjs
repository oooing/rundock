import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import {computed, ref} from 'vue'
const names=['inspectCandidate','cancelCandidate','submitRelease','ignoreSafetyFile','ignoreSafetyFiles']
const functions=[]
for(const file of ['candidate','submission']){
 const source=readFileSync(new URL('../../src/components/release/'+file+'.ts',import.meta.url),'utf8')
 const ast=ts.createSourceFile(file+'.ts',source,ts.ScriptTarget.Latest,true)
 function visit(node){
  if(ts.isBinaryExpression(node) && ts.isPropertyAccessExpression(node.left) &&
     node.left.expression.getText(ast)==='ctx' && names.includes(node.left.name.text)){
   functions.push('const '+node.left.name.text+'='+node.right.getText(ast).replace(/\bctx\./g,''))
  }
  ts.forEachChild(node,visit)
 }
 visit(ast)
}
assert.equal(functions.length,names.length,'test must use the current production functions')
const code=ts.transpileModule(functions.join(';\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
function fixture(){
 const candidate=ref(null),checkingCandidate=ref(false),checks=[],polls=[],timers=[],applied=[],published=[]
 const prepared={id:'test',status:'pending',accepted:false,sensitiveFindings:[],dependencyFindings:[],checkResults:[]}
 const canSubmit=ref(true),candidateReady=computed(()=>!checkingCandidate.value && (candidate.value?.accepted || candidate.value?.status==='ready' && candidate.value?.canSaveProgress))
 const deps={savePreparation:async()=>{},autoSubmitting:ref(false),canSubmit,candidateReady,canPublish:computed(()=>canSubmit.value && candidateReady.value),submissionPlanSignature:ref('original-plan'),publish:async()=>{published.push(candidate.value.id)},nextTick:async()=>{},bodyRef:ref(null),tr:v=>v,checkingCandidate,preflight:ref({}),error:ref(''),disposed:false,props:{app:{id:'app'}},candidate,candidateSignature:ref(''),candidateRequest:ref({intent:'formal'}),profileBody:()=>({}),applyPreflight:value=>applied.push(value),messageOf:String,
 api:{saveReleaseProfile:async()=>{},releasePreflight:async()=>({}),prepareReleaseCandidate:async()=>({...prepared}),checkReleaseCandidate:()=>{const d=deferred();checks.push(d);return d.promise},getReleaseCandidate:()=>{const d=deferred();polls.push(d);return d.promise},cancelReleaseCandidate:async()=>({...prepared,status:'cancelled'})},setTimeout:callback=>{timers.push(callback);return timers.length},clearTimeout:()=>{}}
 Object.assign(deps,{reviewSignature:ref(''),findingDecisions:ref({}),ignoringFile:ref(false),publishing:ref(false),manualDecisions:ref([]),versionPlanNotice:ref(''),candidateNeedsAttention:computed(()=>['blocked','failed'].includes(candidate.value?.status))})
 const actions=new Function(...Object.keys(deps),`let candidateEpoch=0,candidatePoll=null,candidateAbort=null;${code};return {inspectCandidate,cancelCandidate,submitRelease,ignoreSafetyFile,dispose:()=>{disposed=true}}`)(...Object.values(deps))
 return {...actions,...deps,checks,polls,timers,applied,published}
}
async function until(test){for(let i=0;i<30&&!test();i++)await Promise.resolve();assert.ok(test(),'asynchronous stage not reached')}
test('permanent ignore refreshes files without committing or discarding other decisions',async()=>{
 const f=fixture(),pending=deferred(),file={path:'notes/local.txt',contentFingerprint:'same',tracked:false},calls=[]
 f.candidate.value={id:'old',accepted:true};f.candidateSignature.value='old'
 f.manualDecisions.value=[{path:file.path,decision:'exclude'},{path:'other.txt',decision:'include'}]
 f.api.ignoreReleaseFiles=(...args)=>{calls.push(args);return pending.promise}
 const run=f.ignoreSafetyFile(file)
 assert.equal(f.ignoringFile.value,true)
 await assert.rejects(f.ignoreSafetyFile(file))
 assert.equal(await f.inspectCandidate(),false)
 pending.resolve({ignoreFiles:['.gitignore'],preflight:{changes:[]}})
 assert.equal(await run,'.gitignore')
 assert.deepEqual(calls,[['app',[{path:file.path,contentFingerprint:'same'}]]]);assert.equal(f.ignoringFile.value,false)
 assert.equal(f.candidate.value,null);assert.equal(f.candidateSignature.value,'')
 assert.deepEqual(f.manualDecisions.value,[{path:'other.txt',decision:'include'}])
 assert.deepEqual(f.applied,[{changes:[]}]);assert.deepEqual(f.published,[])
})
test('failed ignore preserves review and tracked files never invoke the API',async()=>{
 const f=fixture(),calls=[],file={path:'local.txt',contentFingerprint:'same',tracked:false}
 f.candidate.value={id:'old'};f.manualDecisions.value=[{path:'other.txt',decision:'include'}]
 f.api.ignoreReleaseFiles=async()=>{calls.push('ignore');throw new Error('file changed')}
 await assert.rejects(f.ignoreSafetyFile(file),/file changed/)
 assert.equal(f.ignoringFile.value,false);assert.equal(f.candidate.value.id,'old')
 assert.deepEqual(f.applied,[]);assert.equal(f.manualDecisions.value.length,1)
 await assert.rejects(f.ignoreSafetyFile({...file,tracked:true}))
 assert.deepEqual(calls,['ignore']);assert.deepEqual(f.published,[])
})
test('unconfirmed file choices stop before command checks or publication',async()=>{
 const f=fixture()
 f.api.prepareReleaseCandidate=async()=>({id:'review',status:'blocked',accepted:false,sensitiveFindings:[],dependencyFindings:[],checkResults:[{id:'rundock:scope',status:'blocked',required:true}]})
 await f.submitRelease()
 assert.equal(f.candidate.value.status,'blocked')
 assert.equal(f.checks.length,0)
 assert.deepEqual(f.published,[])
})

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

test('allowing a finding reuses the saved candidate without preflight or copying',async()=>{
 const f=fixture(),original={intent:'formal',sensitiveExceptions:[]},calls=[];
 f.candidateRequest.value={...original,sensitiveExceptions:[{path:'test.py',reason:'fixture'}]};
 f.candidateSignature.value=JSON.stringify(original);
 f.candidate.value={id:'existing',status:'blocked',accepted:false};
 for(const name of ['saveReleaseProfile','releasePreflight','prepareReleaseCandidate'])f.api[name]=async()=>{throw new Error('unexpected full preparation: '+name)};
 f.api.reviewReleaseCandidate=async(...args)=>{calls.push(args);return {id:'existing',status:'pending',accepted:false,sensitiveFindings:[],dependencyFindings:[],checkResults:[]}};
 const run=f.inspectCandidate(true);await until(()=>f.checks.length===1);
 f.checks[0].resolve({id:'existing',status:'passed',accepted:true});assert.equal(await run,true);
 assert.equal(calls.length,1);assert.equal(calls[0][1],'existing');assert.deepEqual(f.published,[]);
})

test('changed file choices rebuild and stale confirmation never silently rebuilds',async()=>{
 const changed=fixture();changed.candidate.value={id:'old',status:'blocked'};changed.candidateSignature.value=JSON.stringify({intent:'formal',selectedPaths:['a']});changed.candidateRequest.value={intent:'formal',selectedPaths:['b']};
 changed.api.reviewReleaseCandidate=async()=>{throw new Error('must not reuse changed selection')};
 const full=changed.inspectCandidate(true);await until(()=>changed.checks.length===1);changed.checks[0].resolve({accepted:true,status:'passed'});await full;
 const stale=fixture();stale.candidate.value={id:'old',status:'blocked'};stale.candidateSignature.value=JSON.stringify({intent:'formal'});
 stale.api.prepareReleaseCandidate=async()=>{throw new Error('must not silently copy after stale review')};
 stale.api.reviewReleaseCandidate=async()=>{throw new Error('source changed; recheck required')};
 assert.equal(await stale.inspectCandidate(true),false);assert.match(stale.error.value,/source changed/);assert.equal(stale.candidate.value.status,'stale');assert.deepEqual(stale.published,[]);
})

test('completed review results do not rerun checks and late reviews cannot overwrite cancellation',async()=>{
 const done=fixture();done.candidate.value={id:'old',status:'passed'};done.candidateSignature.value=JSON.stringify({intent:'formal'});
 done.api.reviewReleaseCandidate=async()=>({id:'old',status:'passed',accepted:true,sensitiveFindings:[],dependencyFindings:[],checkResults:[]});
 assert.equal(await done.inspectCandidate(true),true);assert.equal(done.checks.length,0);
 const f=fixture(),pending=deferred();f.candidate.value={id:'old',status:'blocked'};f.candidateSignature.value=JSON.stringify({intent:'formal'});
 f.api.reviewReleaseCandidate=()=>pending.promise;const run=f.inspectCandidate(true);await Promise.resolve();await f.cancelCandidate();
 pending.resolve({id:'old',status:'passed',accepted:true});await run;assert.equal(f.candidate.value.status,'cancelled');assert.equal(f.checks.length,0);
})

test('a resolved finding waits for confirmation, then uses passed checks without running them again',async()=>{
 const f=fixture();f.candidate.value={id:'reviewed',status:'blocked'};
 f.candidateSignature.value=JSON.stringify({intent:'formal'});
 f.api.reviewReleaseCandidate=async()=>({id:'reviewed',status:'passed',accepted:true,sensitiveFindings:[],dependencyFindings:[],checkResults:[]});
 assert.equal(await f.inspectCandidate(true),true);
 assert.equal(f.candidateReady.value,true);assert.deepEqual(f.published,[]);
 await f.submitRelease();
 assert.deepEqual(f.published,['reviewed']);assert.equal(f.checks.length,0);
})

test('interrupted submission exposes its reason and scrolls to the error, not the check panel',async()=>{
 for(const scenario of ['plan-changed','transport-error','incomplete']) {
  const f=fixture(),scrolls=[];
  f.bodyRef.value={scrollTo:()=>scrolls.push('error'),querySelector:()=>({scrollIntoView:()=>scrolls.push('checks')})};
  const run=f.submitRelease();await until(()=>f.checks.length===1);
  if(scenario==='plan-changed') f.submissionPlanSignature.value='new-plan';
  if(scenario==='transport-error') f.checks[0].reject(new Error('connection lost'));
  else f.checks[0].resolve({id:'result',status:scenario==='incomplete'?'pending':'passed',accepted:scenario!=='incomplete'});
  await run;
  assert.ok(f.error.value,scenario);assert.deepEqual(scrolls,['error'],scenario);
  assert.deepEqual(f.published,[]);assert.equal(f.autoSubmitting.value,false);
 }
})
