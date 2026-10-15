import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import ts from 'typescript'
import { loadMembers } from './helpers/release-members.mjs'

const panelSource = parse(readFileSync(new URL('../../src/components/ReleaseSafetyPanel.vue', import.meta.url), 'utf8')).descriptor.scriptSetup.content
const ast = ts.createSourceFile('panel.ts', panelSource, ts.ScriptTarget.Latest, true)
const code = ts.transpileModule(ast.statements.filter(s => !ts.isImportDeclaration(s)).map(s => s.getText(ast)).join('\n'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
function fixture(ignoreFile, ignoreFiles = async () => { throw Error('Unexpected bulk call') }) {
  const files = ['one.png', 'two.png'].map(path => ({ path, category: 'review', contentFingerprint: path, tracked: false }))
  const props = vue.reactive({ files, selected: {}, decisions: [], candidate: null, busy: false, locked: false, ignoreFile, ignoreFiles })
  const events = [], unmount = [], scope = vue.effectScope()
  const deps = { ...vue, window: { removeEventListener() {} }, useId: () => 'fixture', onMounted() {}, onBeforeUnmount: fn => unmount.push(fn),
    defineProps: () => props, withDefaults: p => p, defineEmits: () => (...args) => events.push(args),
    tr: text => text, hasOnlyFileReviewBlock: () => false }
  const panel = scope.run(() => new Function(...Object.keys(deps), `${code};return {chooseBulk,chooseReview,applyIgnores,skipFailedIgnores,pendingIgnores,ignoreError,ignoring}`)(...Object.values(deps)))
  return { panel, props, events, pending: () => events.filter(e => e[0] === 'ignore-pending').at(-1)[1], close() { unmount.forEach(fn => fn()); scope.stop() } }
}

test('single ignore writes immediately and locks submission until persistence succeeds', async () => {
  const calls = []; let complete
  const f = fixture(async file => { calls.push(file.path); await new Promise(resolve => { complete = resolve }); return '.gitignore' })
  try {
    const saving = f.panel.chooseReview(f.props.files[0], { target: { value: 'ignore' } })
    assert.equal(f.pending(), 1)
    assert.deepEqual(calls, ['one.png'], 'selecting immediately starts writing the rule')
    f.panel.chooseReview(f.props.files[0], { target: { value: 'ignore' } })
    f.panel.chooseBulk({ target: { value: 'ignore' } })
    assert.deepEqual(calls, ['one.png'], 'repeat events cannot start duplicate writes')
    assert.equal(f.pending(), 1, 'keep submission blocked during persistence')
    assert.equal(f.panel.ignoring.value, true)
    complete(); await saving
    assert.equal(f.pending(), 0)
  } finally { f.close() }
})

test('failed bulk request stays locked and retry resends one idempotent batch', async () => {
  const calls = []; let fail = true
  const f = fixture(async () => { throw Error('Unexpected per-file request') }, async files => { calls.push(files.map(file=>file.path)); if (fail) throw Error('Cannot write ignore rules'); return '.gitignore' })
  try {
    await f.panel.chooseBulk({ target: { value: 'ignore' } })
    assert.equal(f.pending(), 2)
    assert.match(f.panel.ignoreError.value, /Cannot write/)
    fail = false; await f.panel.applyIgnores()
    assert.equal(f.pending(), 0)
    assert.deepEqual(calls, [['one.png', 'two.png'], ['one.png', 'two.png']])
  } finally { f.close() }
})

test('664 review files use one bulk request and no per-file requests', async () => {
  const calls = []
  const f = fixture(async () => { throw Error('Unexpected per-file request') }, async files => { calls.push(files.map(file=>file.path)); return '.gitignore' })
  f.props.files=Array.from({length:664},(_,i)=>({path:`frame-${i}.png`,category:'review',tracked:false,contentFingerprint:`hash-${i}`}))
  try {
    await f.panel.chooseBulk({ target: { value: 'ignore' } })
    assert.equal(calls.length, 1)
    assert.deepEqual(calls[0], f.props.files.map(file=>file.path))
    assert.equal(f.pending(), 0)
    assert.equal(f.panel.ignoreError.value, '')
  } finally { f.close() }
})

test('after an ignore failure, skip only excludes remaining files for this submission', async () => {
  const f = fixture(async () => '.gitignore', async () => { throw Error('write failed') })
  try {
    await f.panel.chooseBulk({ target: { value: 'ignore' } })
    f.events.length = 0
    f.panel.skipFailedIgnores()
    assert.deepEqual(f.events.filter(e => e[0] === 'choose').map(e => [e[1].path, e[2]]), [['one.png', false], ['two.png', false]])
    assert.equal(f.pending(), 0)
  } finally { f.close() }
})

test('pending ignore locks the real submission eligibility until resolved', () => {
  const deps = { computed: vue.computed }
  for (const name of ['submissionUncertain','ignoringFile','localBuildOnly','safetySettingsDirty','unstaging','configFileDirty','configEditorOpen','configSaving','configScanning','remoteMissing','savingProfile','preflightStale','activeRun','publishing','createTag']) deps[name] = vue.ref(false)
  Object.assign(deps, { pendingIgnoreCount: vue.ref(0), syncPolicy: vue.ref('always'), preferenceStatus: vue.ref('saved'), localChecksPassed: vue.ref(true), commitMessage: vue.ref('change'), newContentState: vue.ref('new'), targetSelectionValid: vue.ref(true) })
  const { canSubmit } = loadMembers(['canSubmit'], deps)
  assert.equal(canSubmit.value, true)
  deps.pendingIgnoreCount.value = 2; assert.equal(canSubmit.value, false)
  deps.pendingIgnoreCount.value = 1; assert.equal(canSubmit.value, false)
  deps.pendingIgnoreCount.value = 0; assert.equal(canSubmit.value, true)
})

test('bulk controller sends only paths and fingerprints and refreshes the model once', async () => {
  const calls=[], refreshed=[]
  const deps={props:{app:{id:'localplay'}},disposed:false,tr:text=>text,
    ignoringFile:vue.ref(false),checkingCandidate:vue.ref(false),publishing:vue.ref(false),autoSubmitting:vue.ref(false),
    candidate:vue.ref({id:'old'}),candidateSignature:vue.ref('old'),manualDecisions:vue.ref([{path:'frame-0.png'},{path:'keep.txt'}]),
    api:{ignoreReleaseFiles:async(id,files)=>{calls.push({id,files});return {ignoreFiles:['.gitignore'],preflight:{fresh:true}}}},
    applyPreflight:(...args)=>refreshed.push(args)}
  const controller=loadMembers(['ignoreSafetyFile','ignoreSafetyFiles'],deps,{api:deps.api})
  const files=Array.from({length:664},(_,i)=>({path:`frame-${i}.png`,contentFingerprint:`hash-${i}`,tracked:false,category:'review'}))
  await controller.ignoreSafetyFiles(files)
  assert.equal(calls.length,1)
  assert.equal(calls[0].files.length,664)
  assert.deepEqual(Object.keys(calls[0].files[0]).sort(),['contentFingerprint','path'])
  assert.equal(refreshed.length,1)
  assert.equal(deps.candidate.value,null)
  assert.deepEqual(deps.manualDecisions.value,[{path:'keep.txt'}])
  assert.equal(deps.ignoringFile.value,false)
})
