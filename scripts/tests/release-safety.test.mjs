import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
const source=readFileSync(new URL('../../src/utils/releaseSafety.ts',import.meta.url),'utf8')
const compiled=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const api={};new Function('exports',compiled)(api)
const file=(path,category,contentFingerprint='a')=>({path,category,contentFingerprint,selectedDefault:category==='recommend'})
test('refresh retains deliberate excludes, new bytes invalidate only affected decision',()=>{
 const files=[file('src/a.ts','recommend'),file('src/b.ts','recommend','b'),file('notes/private.txt','local')]
 const decisions=files.slice(0,2).map(f=>({path:f.path,contentFingerprint:'a',decision:'exclude'}))
 const result=api.reconcileReleaseSelection(files,decisions)
 assert.deepEqual(result.selected,{'src/a.ts':false,'src/b.ts':true,'notes/private.txt':false})
 assert.equal(result.decisions.length,1)
})
test('recommendations and manual include cannot silently bypass sensitive or local classification',()=>{
 const files=[file('source.ts','recommend'),file('report.txt','local'),file('unknown.txt','review'),file('.env','sensitive')]
 assert.deepEqual(api.recommendedReleaseSelection(files),{'source.ts':true,'report.txt':false,'unknown.txt':false,'.env':false})
 assert.equal(api.reconcileReleaseSelection(files,[{path:'.env',contentFingerprint:'a',decision:'include'}]).selected['.env'],false)
})

test('demo and verification defaults stay unchecked while manual choices survive refresh',()=>{
 const paths=['demos/localplay-ui-static/打开Netflix.cmd','demos/localplay-ui-static-verification/复制到中文目录/localplay-ui-static/打开方案库.cmd']
 const files=paths.map(path=>file(path,'local'))
 assert.deepEqual(api.recommendedReleaseSelection(files),Object.fromEntries(paths.map(path=>[path,false])))
 const decisions=[{path:paths[0],contentFingerprint:'a',decision:'include'}]
 const result=api.reconcileReleaseSelection(files,decisions)
 assert.equal(result.selected[paths[0]],true)
 assert.equal(result.selected[paths[1]],false)
 assert.deepEqual(result.decisions,decisions)
 files[0].contentFingerprint='changed'
 assert.equal(api.reconcileReleaseSelection(files,decisions).selected[paths[0]],false)
})
