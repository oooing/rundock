import assert from 'node:assert/strict'
import test from 'node:test'
import { build } from 'esbuild'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { parse } from '@vue/compiler-sfc'
const result=await build({entryPoints:['src/utils/releaseIssues.ts'],bundle:true,write:false,format:'esm',platform:'node'})
const {groupSensitiveFindings,hasReleaseIssues,releaseCheckPresentation}=await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`)
const candidate=overrides=>({sensitiveFindings:[],dependencyFindings:[],checkResults:[],status:'ready',...overrides})
const {hasOnlyFileReviewBlock}=await import('data:text/javascript;base64,'+Buffer.from(result.outputFiles[0].text).toString('base64'))

test('file confirmation is amber review, including old failed snapshots, never masking real failures',()=>{
 for(const status of ['blocked','failed']){
  const check={id:'rundock:scope',required:true,status,reason:'仍有文件用途需要确认，请在文件范围中处理。'}
  assert.equal(releaseCheckPresentation(check).label,'待确认文件')
  assert.match(releaseCheckPresentation(check).suggestion,/本次不提交.*以后忽略/)
  assert.equal(hasOnlyFileReviewBlock(candidate({status,checkResults:[check]})),true)
  assert.equal(hasOnlyFileReviewBlock(candidate({status,checkResults:[check,{id:'test',required:true,status:'failed'}]})),false)
  assert.equal(hasOnlyFileReviewBlock(candidate({status,checkResults:[check],sensitiveFindings:[{path:'.env'}]})),false)
  for(const state of ['cancelled','stale','ready','running']) assert.equal(hasOnlyFileReviewBlock(candidate({status:state,checkResults:[check]})),false)
 }
 assert.equal(releaseCheckPresentation({id:'rundock:scope',status:'failed',reason:'版本无效'}).label,'检查失败')
})

test('check errors expose the actual reason and distinguish unavailable tools from failed tests',()=>{
 const old=releaseCheckPresentation({status:'unverified',reason:'必需检查工具不可用'});
 assert.equal(old.label,'无法开始检查');assert.match(old.suggestion,/PATH/);assert.doesNotMatch(old.suggestion,/pwsh/);
 const missing=releaseCheckPresentation({status:'unverified',reason:'找不到检查工具：pwsh（PowerShell 7）。'});
 assert.equal(missing.label,'无法开始检查');assert.match(missing.reason,/pwsh/);assert.match(missing.suggestion,/PowerShell 7/);
 const failed=releaseCheckPresentation({status:'failed',reason:'检查超时',log:'operation timed out'});
 assert.equal(failed.label,'检查失败');assert.equal(failed.reason,'检查超时');
 assert.equal(releaseCheckPresentation({status:'blocked',reason:'环境不可用'}).label,'检查被阻止');
 assert.equal(releaseCheckPresentation({status:'unverified',reason:'版本文件被修改'}).label,'尚未验证');
 assert.match(releaseCheckPresentation({status:'failed'}).reason,/没有执行日志/);
 assert.match(releaseCheckPresentation({status:'failed',log:'specific failure'}).reason,/查看执行日志/);
})

test('failure reason and next action are outside collapsed log details',()=>{
 const template=parse(readFileSync('src/components/ReleaseCheckIssues.vue','utf8')).descriptor.template.content;
 assert.match(template,/<p class="check-reason">/);
 assert.match(template,/<p class="check-suggestion">/);
 assert.ok(template.indexOf('class="check-reason"')<template.indexOf('<details v-if="check.log"'));
 assert.doesNotMatch(template,/请根据详情修复/);
})
test('group by file without losing individual finding identities',()=>{
 const findings=[{path:'a',fingerprint:'one'},{path:'b',fingerprint:'two'},{path:'a',fingerprint:'three'}]
 const groups=groupSensitiveFindings(findings)
 assert.equal(groups.length,2)
 assert.deepEqual(groups[0].findings,[findings[0],findings[2]])
 assert.equal(groups[1].findings[0],findings[1])
})
test('warnings do not turn the release button into an error recovery action',()=>{
 assert.equal(hasReleaseIssues(null),false)
 assert.equal(hasReleaseIssues(candidate({dependencyFindings:[{blocked:false}],checkResults:[{required:false,status:'failed'}]})),false)
})
test('secrets, missing dependencies and required failures need recovery',()=>{
 for(const override of [{sensitiveFindings:[{path:'a'}]},{dependencyFindings:[{blocked:true}]},{checkResults:[{required:true,status:'unverified'}]},{status:'failed'}])assert.equal(hasReleaseIssues(candidate(override)),true)
 assert.equal(hasReleaseIssues(candidate({accepted:true,status:'passed'})),false)
})
test('missing-file action can only add recommended files, never protected files or stale results',()=>{
 const source=parse(readFileSync('src/components/ReleaseCheckIssues.vue','utf8')).descriptor.scriptSetup.content
 const ast=ts.createSourceFile('issues.ts',source,ts.ScriptTarget.Latest,true)
 const code=ts.transpileModule(ast.statements.filter(s=>['selectedFile','addableFile','include','exclude'].includes(s.name?.text)).map(s=>s.getText(ast)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText
 const props={files:['recommend','review','local','sensitive'].map(category=>({path:category,category})),selected:{},busy:false,stale:false},calls=[]
 const f=new Function('props','emit',`${code};return {include,exclude}`)(props,(...args)=>calls.push(args))
 for(const category of ['review','local','sensitive','unknown'])f.include(category)
 assert.equal(calls.length,0)
 f.include('recommend');assert.equal(calls.length,1);assert.equal(calls[0][2],true)
 props.stale=true;f.include('recommend');assert.equal(calls.length,1)
 props.selected.recommend=true;props.busy=true;f.exclude('recommend');assert.equal(calls.length,1)
 props.busy=false;f.exclude('recommend');assert.equal(calls.length,2);assert.equal(calls[1][2],false)
})
