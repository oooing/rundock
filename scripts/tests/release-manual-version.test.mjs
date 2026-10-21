import assert from 'node:assert/strict'
import test from 'node:test'
import { nextTick } from 'vue'
import { fixture as model } from './helpers/release-model.mjs'

// Run the actual reactive controller and preflight/submit path. Every API is
// in-memory: these tests never build, commit, tag, upload, or publish a project.
async function fixture({ single = true, onPreflight = () => {}, onPrepare = () => {} } = {}) {
  let snapshot
  const preparedRequests = []
  const f = model({ local: true, single, realVersionSync: true, api: {
    async releasePreflight() {
      await onPreflight(f)
      return structuredClone(snapshot)
    },
    async prepareReleaseCandidate(_, request) {
      preparedRequests.push(structuredClone(request))
      await onPrepare(f, request)
      return { id: 'isolated-check', status: 'passed', accepted: true,
        sensitiveFindings: [], dependencyFindings: [], checkResults: [] }
    },
  } })
  f.preflight.value.classifications = [{ path: 'src/main.ts', category: 'recommend',
    selectedDefault: true, contentFingerprint: 'unchanged-source' }]
  f.changeSyncPolicy('local')
  f.changeLocalVersionMode('upgrade')
  f.releaseNotesDirty.value = true // Keep release notes fixed to isolate version/commit synchronization.
  f.profileReady.value = true
  f.syncVersionInputs()
  f.setDefaultCommitMessage()
  await nextTick()
  await f.saveRememberedPreferences()
  snapshot = JSON.parse(JSON.stringify(f.preflight.value))
  return { ...f, preparedRequests, snapshot }
}

async function settle(f) {
  await nextTick()
  await f.saveRememberedPreferences()
}

test('manual version edits immediately synchronize only the generated commit message', async () => {
  const f = await fixture()
  try {
    f.versionMode.value = 'manual'
    for (const version of ['1.0.2', '1.3.0']) {
      f.versionInputs.value.desktop = version
      assert.equal(f.commitMessage.value, `chore(release): v${version}`)
      assert.equal(f.versionInputs.value.desktop, version, 'do not replace the manual version with the suggestion')
    }
    await settle(f)
  } finally { f.close() }
})

for (const scenario of ['auto', 'manual-same', 'manual-changed', 'back-to-auto']) {
  test(`${scenario}: unchanged source submits once without a false content-change warning`, async () => {
    const f = await fixture()
    try {
      if (scenario !== 'auto') {
        f.versionMode.value = 'manual'
        f.versionInputs.value.desktop = scenario === 'manual-same' ? '1.0.1' : '1.3.0'
      }
      if (scenario === 'back-to-auto') f.versionMode.value = 'auto'
      await settle(f)
      const version = scenario === 'manual-changed' ? '1.3.0' : '1.0.1'
      assert.equal(f.canSubmit.value, true)
      const signature = f.submissionPlanSignature.value
      await f.submitRelease()
      assert.equal(f.error.value, '')
      assert.equal(f.requests.length, 1)
      assert.equal(f.requests[0].targetVersion, version)
      assert.equal(f.requests[0].commitMessage, `chore(release): v${version}`)
      assert.deepEqual(f.requests[0].selectedPaths, ['src/main.ts'])
      assert.equal(f.requests[0].statusFingerprint, f.snapshot.statusFingerprint)
      assert.equal(f.submissionPlanSignature.value, signature)
      assert.deepEqual(JSON.parse(JSON.stringify(f.preflight.value)), f.snapshot)
    } finally { f.close() }
  })
}

test('editing a manual version and submitting in the same tick uses the updated description', async () => {
  const f = await fixture()
  try {
    f.versionMode.value = 'manual'
    await settle(f)
    f.versionInputs.value.desktop = '1.4.0'
    await f.submitRelease()
    assert.equal(f.error.value, '')
    assert.equal(f.requests.length, 1)
    assert.equal(f.requests[0].commitMessage, 'chore(release): v1.4.0')
  } finally { f.close() }
})

test('multiple manually versioned targets keep their matching generated tags and description', async () => {
  const f = await fixture({ single: false })
  try {
    f.versionMode.value = 'manual'
    f.versionInputs.value = { desktop: '1.3.0', android: '2.4.0' }
    await settle(f)
    assert.equal(f.commitMessage.value, 'chore(release): desktop/v1.3.0, android/v2.4.0')
    await f.submitRelease()
    assert.equal(f.error.value, '')
    assert.equal(f.requests.length, 1)
    assert.deepEqual(f.requests[0].versions, [
      { versionGroupId: 'desktop', targetVersion: '1.3.0' },
      { versionGroupId: 'android', targetVersion: '2.4.0' },
    ])
  } finally { f.close() }
})

test('a user-written commit message survives version changes and preflight refresh', async () => {
  const f = await fixture()
  try {
    f.commitMessage.value = 'Release with my own description'
    f.onCommitMessageInput()
    f.versionMode.value = 'manual'
    f.versionInputs.value.desktop = '1.3.0'
    await settle(f)
    await f.submitRelease()
    assert.equal(f.error.value, '')
    assert.equal(f.requests.length, 1)
    assert.equal(f.requests[0].targetVersion, '1.3.0')
    assert.equal(f.requests[0].commitMessage, 'Release with my own description')
    assert.equal(f.commitMessageDirty.value, true)
  } finally { f.close() }
})

for (const change of ['version', 'selected-files', 'release-notes', 'commit-message']) {
  test(`a real ${change} change during checks still blocks publication`, async () => {
    const f = await fixture({ onPreflight(ctx) {
      if (change === 'version') ctx.versionInputs.value.desktop = '1.4.0'
      if (change === 'selected-files') ctx.manualDecisions.value = [{
        path: 'src/main.ts', decision: 'exclude', contentFingerprint: 'unchanged-source',
      }]
      if (change === 'release-notes') ctx.releaseNotes.value = 'Changed after confirmation'
      if (change === 'commit-message') {
        ctx.commitMessage.value = 'Changed after confirmation'
        ctx.onCommitMessageInput()
      }
    } })
    try {
      f.versionMode.value = 'manual'
      f.versionInputs.value.desktop = '1.3.0'
      await settle(f)
      const signature = f.submissionPlanSignature.value
      await f.submitRelease()
      assert.notEqual(f.submissionPlanSignature.value, signature)
      assert.equal(f.error.value, '发布内容已变化，请核对后再次确认。')
      assert.equal(f.requests.length, 0)
    } finally { f.close() }
  })
}

test('source fingerprint remains in the candidate identity and invalidates an accepted check', async () => {
  const f = await fixture()
  try {
    assert.equal(await f.inspectCandidate(), true)
    assert.equal(f.candidateReady.value, true)
    assert.equal(f.preparedRequests[0].statusFingerprint, f.snapshot.statusFingerprint)
    f.preflight.value.statusFingerprint = 'actual-source-change'
    assert.equal(f.candidateStale.value, true)
    assert.equal(f.candidateReady.value, false)
    assert.equal(f.canPublish.value, false)
    assert.equal(f.requests.length, 0)
  } finally { f.close() }
})

test('backend rejection of changed source still stops the submission', async () => {
  const f = await fixture({ onPrepare() { throw new Error('source changed; recheck required') } })
  try {
    f.versionMode.value = 'manual'
    f.versionInputs.value.desktop = '1.3.0'
    await settle(f)
    await f.submitRelease()
    assert.match(f.error.value, /source changed; recheck required/)
    assert.equal(f.requests.length, 0)
  } finally { f.close() }
})
