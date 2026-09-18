import assert from 'node:assert/strict'
import test from 'node:test'
import { build } from 'esbuild'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { parse } from '@vue/compiler-sfc'
const result=await build({entryPoints:['src/utils/releaseIssues.ts'],bundle:true,write:false,format:'esm',platform:'node'})
const {groupSensitiveFindings,hasReleaseIssues}=await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`)
const candidate=overrides=>({sensitiveFindings:[],dependencyFindings:[],checkResults:[],status:'ready',...overrides})
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
