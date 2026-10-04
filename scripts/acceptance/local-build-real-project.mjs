// Optional end-to-end through the same real HTTP service, without altering the registered source.
import assert from 'node:assert/strict'
import { readFileSync, writeFileSync } from 'node:fs'
import { createHash, randomUUID } from 'node:crypto'
import path from 'node:path'
import { gitAt } from './local-build-fixture.mjs'

export async function verifyRealLocalProject({ request, response, evidence, report, pause }) {
  const project = process.env.RUNDOCK_REAL_LOCAL_PROJECT
  if (!project) return
  const target = process.env.RUNDOCK_REAL_LOCAL_TARGET || 'extension-local'
  const before = { head: gitAt(project, 'rev-parse', 'HEAD'), status: gitAt(project, 'status', '--porcelain=v1', '--untracked-files=all'),
    index: gitAt(project, 'ls-files', '--stage'), tags: gitAt(project, 'show-ref', '--tags') }
  const configFile = path.join(project, '.launcher/release.yaml'), configBytes = readFileSync(configFile)
  const config = JSON.parse(configBytes), chosen = config.targets.find(item => item.id === target)
  assert.ok(chosen, `Real target ${target} is configured`)
  const group = config.versionGroups.find(item => item.id === chosen.versionGroup)
  const sourceVersions = group.versionFiles.map(file => [file.path, readFileSync(path.join(project, file.path))])
  const app = await request('/api/apps', 'POST', { name: `Real ${target} acceptance`, cwd: project,
    entryScript: path.join(project, 'code/start.cmd'), adapterType: 'batch' })
  const prep = (await request(`/api/apps/${app.id}/local-builds`)).preparation
  assert.ok(prep.targets.find(item => item.id === target)?.available, JSON.stringify(prep))
  const run = await request(`/api/apps/${app.id}/local-builds`, 'POST', {
    requestId: randomUUID(), configFingerprint: prep.configFingerprint, targetIds: [target],
  })
  console.log(`Real ${target} build ${run.id} started; original source is not edited.`)
  const deadline = Date.now() + 2 * 60 * 60 * 1000
  let view, lastStage = ''
  while (Date.now() < deadline) {
    view = await request(`/api/local-builds/${run.id}`)
    if (view.run.stage !== lastStage) { lastStage = view.run.stage; console.log(`Real ${target}: ${lastStage}`) }
    if (!['queued', 'running', 'pending'].includes(view.run.status)) break
    await pause(2000)
  }
  writeFileSync(path.join(evidence, `${target}-run.json`), JSON.stringify(view, null, 2))
  assert.equal(view.run.status, 'succeeded', `${view.run.errorCode || view.run.stage}: ${view.run.errorMessage || 'See saved run evidence'}`)
  assert.ok(view.artifacts.length >= 2)
  for (const artifact of view.artifacts) {
    assert.ok(artifact.available)
    const res = await response(`/api/local-builds/${run.id}/artifacts/${artifact.id}`)
    assert.equal(res.status, 200)
    const bytes = Buffer.from(await res.arrayBuffer())
    assert.equal(createHash('sha256').update(bytes).digest('hex'), artifact.sha256)
    writeFileSync(path.join(evidence, path.basename(artifact.name)), bytes)
  }
  const after = { head: gitAt(project, 'rev-parse', 'HEAD'), status: gitAt(project, 'status', '--porcelain=v1', '--untracked-files=all'),
    index: gitAt(project, 'ls-files', '--stage'), tags: gitAt(project, 'show-ref', '--tags') }
  assert.deepEqual(after, before)
  assert.deepEqual(readFileSync(configFile), configBytes)
  for (const [file, bytes] of sourceVersions) assert.deepEqual(readFileSync(path.join(project, file)), bytes)
  report.checks.push(`Real existing project ${target}: current-version commands + package verification + durable artifacts + downloads; original Git/version/config unchanged`)
  report.realProject = { project, target, runId: run.id, artifacts: view.artifacts }
}
