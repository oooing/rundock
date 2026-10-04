const DAY_MS = 24 * 60 * 60 * 1000
const WORKFLOW = '.github/workflows/release.yml'

// Plans, installers and synthetic upgrade evidence share a one-day release lifecycle.
// Keep the name allowlist narrow: unrelated reports must never become cleanup targets.
export function isManagedArtifact(artifact) {
  return /^(?:release-plan|launcher-windows|schema-upgrade)-v\d+\.\d+\.\d+(?:[-+][\w.-]+)?$/.test(artifact.name)
}

export function wasPublished(jobs) {
  return jobs.some(job => job.name === 'Publish GitHub Release' && job.conclusion === 'success')
}

export function planCleanup(entries, now = Date.now()) {
  const eligible = entries.filter(entry => entry.run.status === 'completed')
  const newest = eligible.reduce((latest, entry) => {
    if (!latest || Date.parse(entry.run.created_at) > Date.parse(latest.run.created_at)
        || (entry.run.created_at === latest.run.created_at && entry.run.id > latest.run.id)) return entry
    return latest
  }, null)
  return eligible.filter(entry => entry.published || entry.artifact.expired
    || now - Date.parse(entry.artifact.created_at) >= DAY_MS
    || entry.run.id !== newest.run.id)
}

export async function cleanupArtifacts({ github, context, core, now = Date.now() }) {
  const repo = context.repo
  const actions = github.rest.actions
  // Finish pagination before deleting, otherwise shifting pages can skip artifacts.
  const artifacts = await github.paginate(actions.listArtifactsForRepo, { ...repo, per_page: 100 })
  const runs = new Map()
  const jobs = new Map()
  const entries = []
  for (const artifact of artifacts) {
    if (!isManagedArtifact(artifact)) continue
    const runId = artifact.workflow_run?.id
    if (!runId) continue
    if (!runs.has(runId)) {
      const { data } = await actions.getWorkflowRun({ ...repo, run_id: runId })
      runs.set(runId, data)
    }
    const run = runs.get(runId)
    if (run.path !== WORKFLOW || run.status !== 'completed'
        || !['push', 'workflow_dispatch'].includes(run.event)
        || run.head_repository?.full_name?.toLowerCase() !== `${repo.owner}/${repo.repo}`.toLowerCase()) continue
    if (!jobs.has(runId)) {
      jobs.set(runId, await github.paginate(actions.listJobsForWorkflowRun,
        { ...repo, run_id: runId, filter: 'latest', per_page: 100 }))
    }
    entries.push({ artifact, run, published: wasPublished(jobs.get(runId)) })
  }

  let deleted = 0
  for (const entry of planCleanup(entries, now)) {
    // Preserve a run retried after the initial snapshot, including already-finished retries.
    const { data: fresh } = await actions.getWorkflowRun({ ...repo, run_id: entry.run.id })
    if (fresh.status !== 'completed' || fresh.run_attempt !== entry.run.run_attempt) {
      core.info(`Keep artifact ${entry.artifact.id}: run is active or was retried`)
      continue
    }
    try {
      await actions.deleteArtifact({ ...repo, artifact_id: entry.artifact.id })
      deleted++
      core.info(`Deleted temporary artifact ${entry.artifact.id}`)
    } catch (error) {
      if (error.status !== 404) throw error
    }
  }
  core.info(`Deleted ${deleted} temporary artifacts; Release assets, tags, caches and logs are untouched`)
  return { deleted }
}
