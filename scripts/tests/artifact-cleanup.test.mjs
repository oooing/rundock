import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'
import { isManagedArtifact, wasPublished, planCleanup, cleanupArtifacts } from '../artifact-cleanup.mjs'

const now = Date.parse('2026-09-18T12:00:00Z')
const repo = { owner: 'oooing', repo: 'rundock' }
function entry(id, published = false) {
  return {
    artifact: { id, name: `launcher-windows-v2.0.${id}`, created_at: '2026-09-18T10:00:00Z', workflow_run: { id } },
    run: { id, created_at: `2026-09-18T0${id}:00:00Z`, status: 'completed', run_attempt: 1,
      event: 'push', path: '.github/workflows/release.yml', head_repository: { full_name: 'oooing/rundock' } },
    published,
  }
}
const ids = entries => planCleanup(entries, now).map(entry => entry.artifact.id)

test('published artifacts are deleted immediately, with the prior failed copy', () => {
  assert.deepEqual(ids([entry(1), entry(2, true)]), [1, 2])
})
test('failed and manual builds keep only newest run regardless of completion order', () => {
  assert.deepEqual(ids([entry(3), entry(1), entry(2)]), [1, 2])
})
test('release plan, installers and upgrade evidence from the newest run are retained as a group', () => {
  const plan = entry(2)
  plan.artifact.id = 20
  plan.artifact.name = 'release-plan-v2.0.2'
  const evidence = entry(2)
  evidence.artifact.id = 21
  evidence.artifact.name = 'schema-upgrade-v2.0.2'
  assert.deepEqual(ids([entry(1), plan, entry(2), evidence]), [1])
})
test('latest recovery artifacts expire at 24 hours but not 23 hours', () => {
  const item = entry(1)
  item.artifact.created_at = '2026-09-17T13:00:00Z'
  assert.deepEqual(ids([item]), [])
  item.artifact.created_at = '2026-09-17T12:00:00Z'
  assert.deepEqual(ids([item]), [1])
})
test('active builds cannot be deleted or supersede the recovery copy', () => {
  const active = entry(2, true)
  active.run.status = 'in_progress'
  assert.deepEqual(ids([entry(1), active]), [])
})
test('only known artifact families and actual publish success are accepted', () => {
  for (const name of ['release-plan-v2.0.20', 'launcher-windows-v2.0.20', 'schema-upgrade-v2.0.23']) {
    assert.equal(isManagedArtifact({ name }), true)
  }
  for (const name of ['report', 'launcher-windows-other', 'release-plan-secrets', 'schema-upgrade-secrets']) {
    assert.equal(isManagedArtifact({ name }), false)
  }
  assert.equal(wasPublished([{ name: 'Build Windows installers', conclusion: 'success' }]), false)
  assert.equal(wasPublished([{ name: 'Publish GitHub Release', conclusion: 'failure' }]), false)
  assert.equal(wasPublished([{ name: 'Publish GitHub Release', conclusion: 'success' }]), true)
})
function mock(entries, { freshRun, deleteError } = {}) {
  const calls = []
  const gets = new Map()
  const github = {
    rest: { actions: {
      listArtifactsForRepo: 'artifacts', listJobsForWorkflowRun: 'jobs',
      getWorkflowRun: async ({ run_id }) => {
        const count = (gets.get(run_id) || 0) + 1
        gets.set(run_id, count)
        return { data: count > 1 && freshRun ? freshRun : entries.find(e => e.run.id === run_id).run }
      },
      deleteArtifact: async ({ artifact_id }) => { calls.push(artifact_id); if (deleteError) throw deleteError },
    } },
    paginate: async (method, options) => {
      assert.equal(options.per_page, 100)
      if (method === 'artifacts') return entries.map(e => e.artifact)
      assert.equal(options.filter, 'latest')
      return entries.find(e => e.run.id === options.run_id).published
        ? [{ name: 'Publish GitHub Release', conclusion: 'success' }] : []
    },
  }
  return { github, calls, context: { repo }, core: { info() {} }, now }
}
test('API cleanup excludes active, unknown-workflow, fork, PR and unrelated artifacts', async () => {
  const active = entry(2, true); active.run.status = 'in_progress'
  const unknown = entry(3, true); unknown.run.path = '.github/workflows/other.yml'
  const fork = entry(4, true); fork.run.head_repository.full_name = 'someone/rundock'
  const pr = entry(5, true); pr.run.event = 'pull_request'
  const unrelated = entry(6, true); unrelated.artifact.name = 'other-report'
  const api = mock([entry(1, true), active, unknown, fork, pr, unrelated])
  assert.deepEqual(await cleanupArtifacts(api), { deleted: 1 })
  assert.deepEqual(api.calls, [1])
})
test('a retry after planning protects the original artifacts', async () => {
  const item = entry(1, true)
  for (const freshRun of [{ ...item.run, status: 'queued' }, { ...item.run, run_attempt: 2 }]) {
    const api = mock([item], { freshRun })
    await cleanupArtifacts(api)
    assert.deepEqual(api.calls, [])
  }
})

// Failure inventory for the new family: missed cleanup/expiry, deleting another
// workflow's evidence, or deleting evidence while the originating run is active.
test('upgrade evidence follows cleanup, expiry and origin safety rules', async () => {
  const evidence = entry(1, true)
  evidence.artifact.name = 'schema-upgrade-v2.0.23'
  const api = mock([evidence])
  assert.deepEqual(await cleanupArtifacts(api), { deleted: 1 })
  assert.deepEqual(api.calls, [1])

  evidence.published = false
  evidence.artifact.created_at = '2026-09-17T12:00:00Z'
  assert.deepEqual(await cleanupArtifacts(mock([evidence])), { deleted: 1 })

  for (const change of [{ status: 'in_progress' }, { path: '.github/workflows/other.yml' }]) {
    const excluded = { ...evidence, run: { ...evidence.run, ...change } }
    const protectedAPI = mock([excluded])
    assert.deepEqual(await cleanupArtifacts(protectedAPI), { deleted: 0 })
    assert.deepEqual(protectedAPI.calls, [])
  }
})
test('404 cleanup is idempotent, but permission failures remain visible', async () => {
  const entries = [entry(1, true)]
  assert.deepEqual(await cleanupArtifacts(mock(entries, { deleteError: { status: 404 } })), { deleted: 0 })
  await assert.rejects(cleanupArtifacts(mock(entries, { deleteError: { status: 403 } })), e => e.status === 403)
})
test('workflow uses trusted code and minimum permissions', async () => {
  const cleanup = await readFile(new URL('../../.github/workflows/cleanup-artifacts.yml', import.meta.url), 'utf8')
  assert.match(cleanup, /ref: \$\{\{ github.event.repository.default_branch \}\}/)
  assert.match(cleanup, /contents: read/)
  assert.match(cleanup, /actions: write/)
  assert.match(cleanup, /persist-credentials: false/)
  assert.doesNotMatch(cleanup, /download-artifact|head_sha|packages: write/)
})
test('every upload has one day expiry and cleanup tests run in the release checks', async () => {
  const release = await readFile(new URL('../../.github/workflows/release.yml', import.meta.url), 'utf8')
  const steps = release.split(/(?=^\s*- (?:name|uses):)/m)
  const uploads = steps.filter(step => /uses: actions\/upload-artifact@/.test(step))
  const families = uploads.map(step => step.match(/^\s+name: ([a-z-]+)-\$\{\{/m)?.[1]).sort()
  assert.deepEqual(families, ['launcher-windows', 'release-plan', 'schema-upgrade'])
  for (const family of families) assert.equal(isManagedArtifact({ name: `${family}-v2.0.23` }), true)
  for (const step of uploads) assert.match(step, /^\s+retention-days: 1\s*$/m)
  assert.match(release, /node --test scripts\/tests\/artifact-cleanup.test.mjs/)
})
