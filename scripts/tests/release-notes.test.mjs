import assert from 'node:assert/strict'
import test from 'node:test'
import { nextTick } from 'vue'
import { fixture } from './helpers/release-model.mjs'

const flush = async () => { await nextTick(); await Promise.resolve() }
function setup(t) {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const drafts = []
  const f = fixture({ single: true, realNotes: true, api: {
    releasePreflight: async () => JSON.parse(JSON.stringify(f.preflight.value)),
    getReleaseConfig: async () => JSON.parse(JSON.stringify(f.releaseConfig.value)),
    listReleases: async () => [], listReleasePreparations: async () => [],
    createReleaseNotesDraft(id, body, signal) {
      let resolve, reject
      // Deliberately allow a response after abort: the UI must also guard it.
      const promise = new Promise((yes, no) => { resolve = yes; reject = no })
      drafts.push({ id, body: structuredClone(body), signal, resolve, reject })
      return promise
    },
  } })
  f.releaseNotes.value = ''
  f.loading.value = false
  f.profileReady.value = true
  t.after(f.close)
  const success = (n, text = 'Generated notes') => drafts[n].resolve({ text, baseTag: 'v1.0.0', sourceFingerprint: `draft-${n}` })
  const type = text => { f.releaseNotes.value = text; f.onReleaseNotesInput() }
  return { f, drafts, success, type }
}

test('initial loading finishes independently of an unresolved notes request', { timeout: 2000 }, async t => {
  const { f, drafts } = setup(t)
  const loading = f.load()
  assert.equal(f.loading.value, true)
  await loading
  t.mock.timers.tick(300)
  assert.equal(drafts.length, 1)
  assert.equal(f.releaseNotesLoading.value, true)
  assert.equal(f.loading.value, false)
  assert.equal(f.profileReady.value, true)
})

test('background drafts do not lock the ready form, versions or selected files', async t => {
  const { f, drafts, success } = setup(t)
  f.scheduleReleaseNotesDraft()
  assert.equal(f.releaseNotesLoading.value, true)
  assert.equal(f.loading.value, false)
  assert.equal(f.profileReady.value, true)
  assert.equal(f.publishing.value, false)
  assert.equal(f.canSubmit.value, false, 'missing notes must still block publication')
  t.mock.timers.tick(300)
  f.versionMode.value = 'manual'
  f.onVersionInput('desktop', '1.8.0')
  f.selected.value = { 'src/main.ts': false }
  assert.equal(f.versionInputs.value.desktop, '1.8.0')
  assert.deepEqual(f.selectedPaths.value, [])
  assert.equal(drafts[0].signal.aborted, true)
  t.mock.timers.tick(300)
  assert.equal(drafts.length, 2)
  assert.deepEqual(drafts[1].body.selectedPaths, [])
  success(0, 'Obsolete notes'); await flush()
  assert.equal(f.releaseNotes.value, '')
  assert.equal(f.releaseNotesLoading.value, true, 'old response cannot finish the new request')
  success(1, 'Latest choices'); await flush()
  assert.equal(f.releaseNotes.value, 'Latest choices')
  assert.equal(f.releaseNotesLoading.value, false)
})

test('quick changes are debounced and manual input cancels a queued draft', async t => {
  const { f, drafts, type } = setup(t)
  f.scheduleReleaseNotesDraft()
  t.mock.timers.tick(100)
  f.versionMode.value = 'manual'
  f.onVersionInput('desktop', '1.2.0')
  t.mock.timers.tick(100)
  f.onVersionInput('desktop', '1.3.0')
  t.mock.timers.tick(299)
  assert.equal(drafts.length, 0)
  type('My description')
  t.mock.timers.tick(1000)
  assert.equal(drafts.length, 0)
  assert.equal(f.releaseNotesLoading.value, false)
  assert.equal(f.releaseNotesDirty.value, true)
})

for (const outcome of ['success', 'failure']) {
  test(`typing is preserved after a late ${outcome}, including an intentionally empty field`, async t => {
    const { f, drafts, success, type } = setup(t)
    const pending = f.generateReleaseNotesDraft()
    type('My changes')
    type('')
    assert.equal(drafts[0].signal.aborted, true)
    if (outcome === 'success') success(0)
    else drafts[0].reject(Error('Late network error'))
    await pending
    assert.equal(f.releaseNotes.value, '')
    assert.equal(f.releaseNotesDirty.value, true)
    assert.equal(f.releaseNotesError.value, '')
    assert.equal(f.releaseNotesLoading.value, false)
    f.versionMode.value = 'manual'
    f.onVersionInput('desktop', '1.7.0')
    t.mock.timers.tick(1000)
    assert.equal(drafts.length, 1, 'option changes do not restart generation over manual edits')
  })
}

test('regenerating manual notes requires confirmation and subsequent typing still wins', async t => {
  const { f, drafts, success, type } = setup(t)
  type('Keep this')
  await f.generateReleaseNotesDraft(true)
  assert.equal(f.confirmAction.value, 'regenerate-notes')
  assert.equal(drafts.length, 0)
  const confirmed = f.generateReleaseNotesDraft(true, true)
  success(0, 'Confirmed replacement'); await confirmed
  assert.equal(f.releaseNotes.value, 'Confirmed replacement')
  type('New edits')
  const second = f.generateReleaseNotesDraft(true, true)
  type('Edits after confirmation')
  success(1); await second
  assert.equal(f.releaseNotes.value, 'Edits after confirmation')
  assert.equal(f.releaseNotesDirty.value, true)
})

test('stale errors cannot overwrite the latest draft status', async t => {
  const { f, drafts, success } = setup(t)
  const old = f.generateReleaseNotesDraft()
  f.selected.value = { 'src/main.ts': false }
  t.mock.timers.tick(300)
  drafts[0].reject(Error('Obsolete error')); await old
  assert.equal(f.releaseNotesError.value, '')
  assert.equal(f.releaseNotesLoading.value, true)
  success(1); await flush()
  assert.equal(f.releaseNotes.value, 'Generated notes')
})

test('current generation failure stays local and allows writing notes or retrying', async t => {
  const { f, drafts, success, type } = setup(t)
  const first = f.generateReleaseNotesDraft()
  drafts[0].reject(Error('Network unavailable')); await first
  assert.equal(f.releaseNotesError.value, 'Network unavailable')
  assert.equal(f.error.value, '')
  assert.equal(f.loading.value, false)
  assert.equal(f.profileReady.value, true)
  const retry = f.generateReleaseNotesDraft(true)
  success(1); await retry
  assert.equal(f.releaseNotesError.value, '')
  type('Write it myself')
  assert.equal(f.releaseNotes.value, 'Write it myself')
})

for (const stop of ['close', 'save-code', 'no-platform', 'active-run']) {
  test(`${stop} cancels drafts and rejects late responses`, async t => {
    const { f, drafts, success } = setup(t)
    const pending = f.generateReleaseNotesDraft()
    if (stop === 'close') { f.model.disposed = true; f.close() }
    if (stop === 'save-code') f.releaseIntent.value = 'save-progress'
    if (stop === 'no-platform') f.targetChoices.value.desktop.selected = false
    if (stop === 'active-run') f.activeRun.value = { id: 'running', status: 'running' }
    await flush()
    assert.equal(drafts[0].signal.aborted, true)
    success(0); await pending
    t.mock.timers.tick(1000)
    assert.equal(f.releaseNotes.value, '')
    assert.equal(f.releaseNotesLoading.value, false)
    assert.equal(drafts.length, 1)
  })
}

test('text changed outside the input event is not silently replaced', async t => {
  const { f, success } = setup(t)
  const pending = f.generateReleaseNotesDraft()
  f.releaseNotes.value = 'Restored draft'
  success(0); await pending
  assert.equal(f.releaseNotes.value, 'Restored draft')
})
