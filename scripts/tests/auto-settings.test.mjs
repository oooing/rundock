import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { reactive } from 'vue'
const source = readFileSync(new URL('../../src/utils/autoSettings.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source.replace(/^import .*\n/gm, ''), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const exports = {}
new Function('exports','reactive',compiled)(exports,reactive)
const { createAutoSettings } = exports

test('loading does not save; edits patch only their own key and serialize rapid changes', async () => {
  const writes = [], saved = {grace_period_seconds:'8',closeBehavior:'minimize'}
  let release
  const s = createAutoSettings({getSettings:async()=>saved,setSettings:async patch=>{
    writes.push(patch)
    if(writes.length===1) await new Promise(resolve=>{release=resolve})
    Object.assign(saved,patch)
  }})
  await s.load()
  assert.equal(writes.length,0)
  const first = s.set('grace_period_seconds','1')
  await Promise.resolve()
  const second = s.set('grace_period_seconds','12')
  assert.equal(writes.length,1)
  release(); await Promise.all([first,second])
  assert.deepEqual(writes,[{grace_period_seconds:'1'},{grace_period_seconds:'12'}])
  assert.equal(saved.grace_period_seconds,'12')
  assert.equal(saved.closeBehavior,'minimize')
  assert.equal(s.state.fields.grace_period_seconds.status,'saved')
})

test('invalid edits are not saved and failed saves can be retried', async () => {
  let failing=true,writes=0
  const s=createAutoSettings({getSettings:async()=>({}),setSettings:async()=>{writes++;if(failing)throw Error('offline')}})
  await s.load()
  for(const value of ['', '0', '-1', '1.5', '121'])await s.set('grace_period_seconds',value)
  assert.equal(writes,0)
  assert.equal(s.state.fields.grace_period_seconds.status,'invalid')
  await s.set('grace_period_seconds','15')
  assert.equal(s.state.fields.grace_period_seconds.status,'error')
  assert.match(s.state.fields.grace_period_seconds.error,/offline/)
  failing=false;await s.set('grace_period_seconds','15')
  assert.equal(s.state.fields.grace_period_seconds.status,'saved')
})

test('a pending save does not erase a newer invalid edit or its error state',async()=>{
  let release
  const s=createAutoSettings({getSettings:async()=>({}),setSettings:()=>new Promise(resolve=>{release=resolve})})
  await s.load()
  const pending=s.set('url_discover_timeout_seconds','60');await Promise.resolve()
  await s.set('url_discover_timeout_seconds','')
  release();await pending
  assert.equal(s.state.values.url_discover_timeout_seconds,'')
  assert.equal(s.state.fields.url_discover_timeout_seconds.status,'invalid')
})
