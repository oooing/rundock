import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import {computed, reactive, ref} from 'vue'
import {parse} from '@vue/compiler-sfc'

const source=parse(readFileSync(new URL('../../src/components/ReleaseSafetyPanel.vue',import.meta.url),'utf8')).descriptor.scriptSetup.content
const ast=ts.createSourceFile('panel.ts',source,ts.ScriptTarget.Latest,true)
const names=['search','advancedOpen','reviewOnly','reviewDecision','openReview','unresolved','excludePending']
const code=ts.transpileModule(ast.statements.filter(s=>names.includes(s.name?.text)
 || ts.isVariableStatement(s) && s.declarationList.declarations.some(d=>names.includes(d.name.getText(ast))))
 .map(s=>s.getText(ast)).join('\n'),{compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText

test('review opens directly; content changes invalidate confirmation and bulk exclusion only touches unresolved files',()=>{
 const props=reactive({files:[{path:'a',category:'review',contentFingerprint:'new'},{path:'b',category:'review',contentFingerprint:'same'},{path:'c',category:'recommend',contentFingerprint:'same'}],decisions:[{path:'a',contentFingerprint:'old',decision:'include'},{path:'b',contentFingerprint:'same',decision:'include'}]})
 const calls=[]
 const f=new Function('props','computed','ref','emit',`${code}; return {${names.join(',')}}`)(props,computed,ref,(...args)=>calls.push(args))
 f.search.value='previous search'; f.openReview()
 assert.equal(f.search.value,''); assert.equal(f.advancedOpen.value,true); assert.equal(f.reviewOnly.value,true)
 assert.equal(f.reviewDecision(props.files[0]),undefined)
 assert.equal(f.reviewDecision(props.files[1]),'include')
 assert.deepEqual(f.unresolved.value.map(f=>f.path),['a'])
 f.excludePending()
 assert.deepEqual(calls.map(([event,file,include])=>[event,file.path,include]),[['choose','a',false]])
 props.decisions.push({path:'a',contentFingerprint:'new',decision:'exclude'})
 assert.equal(f.reviewDecision(props.files[0]),'exclude'); assert.equal(f.unresolved.value.length,0)
})
