import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import {computed, ref} from 'vue'
const names=['inspectCandidate','cancelCandidate','submitRelease','ignoreSafetyFile']
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
 const deps={autoSubmitting:ref(false),canSubmit,candidateReady,canPublish:computed(()=>canSubmit.value && candidateReady.value),submissionPlanSignature:ref('original-plan'),publish:async()=>{published.push(candidate.value.id)},nextTick:async()=>{},bodyRef:ref(null),tr:v=>v,checkingCandidate,preflight:ref({}),error:ref(''),disposed:false,props:{app:{id:'app'}},candidate,candidateSignature:ref(''),candidateRequest:ref({intent:'formal'}),profileBody:()=>({}),applyPreflight:value=>applied.push(value),messageOf:String,
 api:{saveReleaseProfile:async()=>{},releasePreflight:async()=>({}),prepareReleaseCandidate:async()=>({...prepared}),checkReleaseCandidate:()=>{const d=deferred();checks.push(d);return d.promise},getReleaseCandidate:()=>{const d=deferred();polls.push(d);return d.promise},cancelReleaseCandidate:async()=>({...prepared,status:'cancelled'})},setTimeout:callback=>{timers.push(callback);return timers.length},clearTimeout:()=>{}}
 Object.assign(deps,{reviewSignature:ref(''),findingDecisions:ref({}),ignoringFile:ref(false),publishing:ref(false),manualDecisions:ref([])})
 const actions=new Function(...Object.keys(deps),`let candidateEpoch=0,candidatePoll=null,candidateAbort=null;${code};return {inspectCandidate,cancelCandidate,submitRelease,ignoreSafetyFile,dispose:()=>{disposed=true}}`)(...Object.values(deps))
 return {...actions,...deps,checks,polls,timers,applied,published}
}
async function until(test){for(let i=0;i<30&&!test();i++)await Promise.resolve();assert.ok(test(),'asynchronous stage not reached')}
test('permanent ignore refreshes files without committing or discarding other decisions',async()=>{
 const f=fixture(),pending=deferred(),file={path:'notes/local.txt',contentFingerprint:'same',tracked:false},calls=[]
 f.candidate.value={id:'old',accepted:true};f.candidateSignature.value='old'
 f.manualDecisions.value=[{path:file.path,decision:'exclude'},{path:'other.txt',decision:'include'}]
 f.api.ignoreReleaseFile=(...args)=>{calls.push(args);return pending.promise}
 const run=f.ignoreSafetyFile(file)
 assert.equal(f.ignoringFile.value,true)
 await assert.rejects(f.ignoreSafetyFile(file))
 assert.equal(await f.inspectCandidate(),false)
 pending.resolve({ignoreFile:'.gitignore',preflight:{changes:[]}})
 assert.equal(await run,'.gitignore')
 assert.deepEqual(calls,[['app',file.path,'same']]);assert.equal(f.ignoringFile.value,false)
 assert.equal(f.candidate.value,null);assert.equal(f.candidateSignature.value,'')
 assert.deepEqual(f.manualDecisions.value,[{path:'other.txt',decision:'include'}])
 assert.deepEqual(f.applied,[{changes:[]}]);assert.deepEqual(f.published,[])
})
test('failed ignore preserves review and tracked files never invoke the API',async()=>{
 const f=fixture(),calls=[],file={path:'local.txt',contentFingerprint:'same',tracked:false}
 f.candidate.value={id:'old'};f.manualDecisions.value=[{path:'other.txt',decision:'include'}]
 f.api.ignoreReleaseFile=async()=>{calls.push('ignore');throw new Error('file changed')}
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
