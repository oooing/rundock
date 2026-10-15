import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse } from '@vue/compiler-sfc'
import { ref } from 'vue'
import { load } from './helpers/release-model.mjs'

const sfc = parse(readFileSync(new URL('../../src/components/ReleaseModal.vue', import.meta.url), 'utf8')).descriptor
function fixture(save) {
  const status = ref('idle'), error = ref(''), push = ref('auto'), storage = new Map(), calls = []
  const ctx = { props: { app: { id: 'test' } }, disposed: false, preferenceRevision: 0, preferenceTimer: null,
    preferenceSave: Promise.resolve(), preferenceStatus: status, preferenceError: error, profileReady: ref(true),
    buildMode: ref('github'), configuredTargets: ref([]), targetChoices: ref({}), syncPolicy: push,
    emit: (...args) => calls.push(args), messageOf: e => e.message }
  for (const key of ['localSyncPolicy','releaseIntent','localVersionMode','versionMode','checksEnabled','publishing','autoSubmitting','checkingCandidate','retrying','activeRun']) ctx[key]=ref(null)
  load('components/release/preferences', {api: {saveReleaseProfile:save}, tr: text=>text, readReleasePreferences:()=>({}),
    writeReleasePreferences:(key,value)=>storage.set(key,JSON.stringify(value)),setTimeout:()=>1,clearTimeout:()=>{}}).installPreferences(ctx)
  ctx.profileBody=()=>({buildMode:ctx.buildMode.value})
  return { ...ctx, status, error, push, storage, calls }
}
test('autosave reports success only after persistence and keeps latest rapid choice', async () => {
  let resolveFirst
  let n=0
  const f=fixture(() => ++n===1 ? new Promise(resolve=>{resolveFirst=resolve}) : Promise.resolve())
  f.rememberPreferences();const first=f.saveRememberedPreferences()
  await new Promise(resolve=>setImmediate(resolve))
  assert.equal(f.status.value,'saving');assert.equal(f.storage.size,0)
  f.push.value='local';f.rememberPreferences();const last=f.saveRememberedPreferences()
  resolveFirst();await first
  assert.equal(f.status.value,'saving','old response must not mark newer choice saved')
  await last
  assert.equal(f.status.value,'saved');assert.equal(JSON.parse(f.storage.get('test')).syncPolicy,'local')
  await f.closeModal();assert.deepEqual(f.calls,[['close']])
})
test('failed autosave stays visible without trapping the dialog; retry persists', async () => {
  let fail=true
  const f=fixture(async()=>{if(fail)throw Error('offline')})
  f.rememberPreferences();await f.saveRememberedPreferences()
  assert.equal(f.status.value,'error');assert.equal(f.error.value,'offline');assert.equal(f.storage.size,0)
  await f.closeModal();assert.deepEqual(f.calls,[['close']]);assert.equal(f.status.value,'error')
  fail=false;await f.saveRememberedPreferences();await f.closeModal()
  assert.equal(f.status.value,'saved');assert.deepEqual(f.calls,[['close'],['close']])
})
test('settings has no footer navigation or cancel; advanced editors retain explicit save', () => {
  assert.match(sfc.template.content, /<footer v-if="[^"\n]*releaseTab === 'publish'/)
  assert.doesNotMatch(sfc.template.content, /return-to-release/)
  assert.match(sfc.template.content, /role="status" aria-live="polite"/)
  assert.match(sfc.template.content, /@click="saveReleaseConfig"/)
})
