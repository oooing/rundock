import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { parse } from '@vue/compiler-sfc'
import { ref } from 'vue'

const sfc = parse(readFileSync(new URL('../../src/components/ReleaseModal.vue', import.meta.url), 'utf8')).descriptor
const ast = ts.createSourceFile('modal.ts', sfc.scriptSetup.content, ts.ScriptTarget.Latest, true)
const names = ['rememberPreferences', 'saveRememberedPreferences', 'closeModal']
const code = ts.transpileModule(ast.statements.filter(s => names.includes(s.name?.text)).map(s => s.getText(ast)).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
function fixture(save) {
  const status = ref('idle'), error = ref(''), push = ref(true), storage = new Map(), calls = []
  const setup = new Function('ref', 'api', 'localStorage', 'preferenceStatus', 'preferenceError', 'pushRemote', 'emit', `
    const profileReady=ref(true),buildMode=ref('github'),createTag=ref(true),versionMode=ref('auto'),publishing=ref(false);
    const props={app:{id:'test'}};let disposed=false,preferenceRevision=0,preferenceTimer=null,preferenceSave=Promise.resolve();
    const preferenceKey=()=> 'test';const profileBody=()=>({buildMode:buildMode.value});const messageOf=e=>e.message;
    const setTimeout=()=>1,clearTimeout=()=>{};
    ${code}
    return {${names.join(',')}};
  `)(ref, { saveReleaseProfile: save }, { setItem: (k,v) => storage.set(k,v) }, status, error, push, (...args) => calls.push(args))
  return { ...setup, status, error, push, storage, calls }
}
test('autosave reports success only after persistence and keeps latest rapid choice', async () => {
  let resolveFirst
  let n=0
  const f=fixture(() => ++n===1 ? new Promise(resolve=>{resolveFirst=resolve}) : Promise.resolve())
  f.rememberPreferences();const first=f.saveRememberedPreferences()
  await new Promise(resolve=>setImmediate(resolve))
  assert.equal(f.status.value,'saving');assert.equal(f.storage.size,0)
  f.push.value=false;f.rememberPreferences();const last=f.saveRememberedPreferences()
  resolveFirst();await first
  assert.equal(f.status.value,'saving','old response must not mark newer choice saved')
  await last
  assert.equal(f.status.value,'saved');assert.equal(JSON.parse(f.storage.get('test')).pushRemote,false)
  await f.closeModal();assert.deepEqual(f.calls,[['close']])
})
test('failed autosave remains visible; retry persists before closing', async () => {
  let fail=true
  const f=fixture(async()=>{if(fail)throw Error('offline')})
  f.rememberPreferences();await f.saveRememberedPreferences()
  assert.equal(f.status.value,'error');assert.equal(f.error.value,'offline');assert.equal(f.storage.size,0)
  await f.closeModal();assert.equal(f.calls.length,0)
  fail=false;await f.saveRememberedPreferences();await f.closeModal()
  assert.equal(f.status.value,'saved');assert.deepEqual(f.calls,[['close']])
})
test('settings has no footer navigation or cancel; advanced editors retain explicit save', () => {
  assert.match(sfc.template.content, /<footer v-if="[^"\n]*releaseTab === 'publish'/)
  assert.doesNotMatch(sfc.template.content, /return-to-release/)
  assert.match(sfc.template.content, /role="status" aria-live="polite"/)
  assert.match(sfc.template.content, /@click="saveReleaseConfig"/)
})
