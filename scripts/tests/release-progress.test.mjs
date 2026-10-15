import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
const source = readFileSync(new URL('../../src/utils/releaseProgress.ts', import.meta.url), 'utf8')
const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const exports = {}; new Function('exports', code)(exports)
const { releaseProgress, targetProgress, formatArtifactSize, artifactSizeSummary } = exports
const target = { targetId: 'android-local', build: true, package: false, publish: true, deploy: false, status: 'waiting', stage: 'waiting_publish', buildDone: true }
const run = { selectedTargets: [target], status: 'running', stage: 'delivery_publish', pushRemote: true, createTag: true, errorCode: '', errorMessage: '' }
const deliveries = [{ groupId: 'android', state: 'uploading', syncState: 'unconfigured' }, { groupId: 'desktop', state: 'sealed', syncState: 'unconfigured' }]
test('sizes distinguish zero, missing, bytes, KB, MB and GB', () => {
  for (const missing of [null, undefined, NaN, Infinity, -1, '10']) assert.equal(formatArtifactSize(missing), '—')
  for (const [size, expected] of [[0, '0 B'], [428, '428 B'], [1024, '1 KB'], [1048576, '1 MB'], [1073741824, '1 GB'], [88430720, '84.3 MB']]) assert.equal(formatArtifactSize(size), expected)
  assert.deepEqual(artifactSizeSummary([{ sizeBytes: 1024 }, { sizeBytes: 1024 }, {}, { sizeBytes: -1 }]), { size: '2 KB', missing: 2 })
  assert.deepEqual(artifactSizeSummary([{}]), { size: '—', missing: 1 })
  assert.deepEqual(artifactSizeSummary([{ sizeBytes: 0 }]), { size: '0 B', missing: 0 })
})
test('upload is visible at the top and has a future Release step', () => {
  const view = releaseProgress({ run, deliveries })
  assert.equal(view.title, '正在上传 GitHub Release')
  assert.equal(view.next, '正式发布 Release')
  assert.equal(view.published, 0)
  assert.equal(view.state, 'running')
  assert.equal(view.steps.find(x => x.id === 'push').state, 'succeeded')
  assert.equal(view.steps.find(x => x.id === 'release').state, 'waiting')
})
test('one published group cannot imply all assets or releases have completed', () => {
  const view = releaseProgress({ run, deliveries: [{ ...deliveries[0], state: 'published' }, { ...deliveries[1], state: 'publishing' }, { groupId: 'web', state: 'sealed' }] })
  assert.equal(view.title, '正在确认 Release 发布')
  assert.equal(view.published, 1)
  assert.notEqual(view.steps.find(x => x.id === 'upload').state, 'succeeded')
  assert.notEqual(view.steps.find(x => x.id === 'complete').state, 'succeeded')
})
test('targets use actual uploading delivery instead of stale waiting_publish', () => {
  const view = targetProgress(target, deliveries, [{ id: target.targetId, versionGroup: 'android' }])
  assert.equal(view.state, 'running'); assert.equal(view.label, '正在上传 GitHub Release')
  assert.equal(targetProgress({ ...target, publish: false }, deliveries, [{ id: target.targetId, versionGroup: 'android' }]).state, 'waiting')
})
test('failure identifies the failed step and error, without completing later stages', () => {
  const view = releaseProgress({ run: { ...run, status: 'failed', errorMessage: '网络连接中断' }, deliveries: [{ ...deliveries[0], state: 'failed', errorMessage: '上传失败：网络超时' }] })
  assert.equal(view.state, 'failed'); assert.equal(view.error, '上传失败：网络超时')
  assert.equal(view.phaseLabel, '上传并核验 GitHub 附件')
  assert.equal(view.steps.at(-1).state, 'waiting')
})
test('published does not claim server synchronization', () => {
  const view = releaseProgress({ run: { ...run, status: 'succeeded', stage: 'completed' }, deliveries: deliveries.map(x => ({ ...x, state: 'published', syncState: 'pending' })) })
  assert.equal(view.state, 'succeeded'); assert.equal(view.syncPending, true)
  assert.equal(view.title, '构建与发布已完成')
})
test('code pushed or cloud handoff is not a successful Release', () => {
  const codeOnly = releaseProgress({ run: { ...run, selectedTargets: [], createTag: false, status: 'succeeded', stage: 'completed' } })
  assert.equal(codeOnly.title, '本次操作已完成')
  assert.ok(!codeOnly.steps.some(x => x.id === 'release'))
  const cloud = releaseProgress({ run: { ...run, status: 'succeeded', stage: 'completed' }, cloudHandoff: true, cloudBuild: { state: 'pending' } })
  assert.equal(cloud.state, 'running'); assert.equal(cloud.title, '等待云端结果')
  assert.equal(cloud.steps.at(-1).state, 'waiting')
  const incomplete = releaseProgress({ run: { ...run, status: 'succeeded', stage: 'completed' }, deliveries })
  assert.equal(incomplete.state, 'running')
  assert.equal(incomplete.steps.find(x => x.id === 'release').state, 'waiting')
})
test('local-only steps never promise remote pushes or GitHub uploads', () => {
  const view = releaseProgress({ run: { ...run, pushRemote: false, stage: 'local_build' }, localOnly: true })
  assert.deepEqual(view.steps.map(x => x.id), ['prepare', 'build', 'verify', 'complete'])
  assert.ok(view.steps.every(x => !/GitHub|Release|推送/.test(x.label)))
})
test('queued targets are waiting, and cancellation is not failure', () => {
  assert.equal(targetProgress({ ...target, status: 'queued', stage: 'waiting' }, [], []).state, 'waiting')
  const view = releaseProgress({ run: { ...run, status: 'failed', errorCode: 'delivery_cancelled' }, deliveries })
  assert.equal(view.state, 'cancelled'); assert.equal(view.title, '本次操作已取消')
})

test('all successful cloud workflows resolve stale handoff rows in the same release', () => {
  const cloudBuild = { releaseRunId: 'release-1', state: 'succeeded' }
  const targets = ['android', 'desktop', 'web-server'].map(targetId => ({ ...target, targetId, releaseRunId: 'release-1', status: 'handed_off', stage: 'cloud_pending' }))
  const view = releaseProgress({ run: { ...run, status: 'succeeded', stage: 'completed' }, targets, cloudHandoff: true, cloudBuild })
  assert.equal(view.state, 'succeeded')
  for (const row of targets) {
    assert.equal(targetProgress(row, [], [], cloudBuild).state, 'succeeded')
    assert.equal(targetProgress(row, [], [], cloudBuild).label, '云端构建已完成')
    assert.equal(row.status, 'handed_off', 'keep original dispatch history intact')
  }
})

test('unknown, partial, failed, superseded or unrelated cloud results cannot complete target rows', () => {
  const row = { ...target, releaseRunId: 'release-1', status: 'handed_off', stage: 'cloud_pending' }
  for (const state of ['pending', 'running', 'failed', 'unavailable', 'not_started', 'superseded']) {
    assert.equal(targetProgress(row, [], [], { releaseRunId: 'release-1', state }).state, 'waiting')
  }
  assert.equal(targetProgress(row, [], [], null).state, 'waiting')
  assert.equal(targetProgress(row, [], [], { releaseRunId: 'another-release', state: 'succeeded' }).state, 'waiting')
  assert.equal(targetProgress({ ...row, status: 'failed' }, [], [], { releaseRunId: 'release-1', state: 'succeeded' }).state, 'failed')
  assert.equal(targetProgress({ ...target, releaseRunId: 'release-1' }, deliveries, [{ id: target.targetId, versionGroup: 'android' }], { releaseRunId: 'release-1', state: 'succeeded' }).state, 'running')
})

test('pre-build GitHub failures never mark an unstarted build or upload complete', () => {
  for (const stage of ['delivery_connecting', 'delivery_preflight']) {
    const view = releaseProgress({ run: { ...run, stage, status: 'failed', commitSha: '', errorCode: 'github_request_unconfirmed' }, targets: [{ ...target, buildDone: false }], deliveryPlanned: true })
    assert.equal(view.phaseLabel, '连接 GitHub')
    assert.equal(view.steps[0].state, 'failed')
    assert.ok(view.steps.slice(1).every(step => step.state === 'waiting'))
    assert.ok(view.steps.some(step => step.id === 'release'))
  }
  const postBuild = releaseProgress({ run: { ...run, stage: 'delivery_preflight', status: 'failed', commitSha: 'saved' }, deliveries })
  assert.equal(postBuild.steps.find(step => step.id === 'build').state, 'succeeded')
  assert.equal(postBuild.steps.find(step => step.id === 'verify').state, 'failed')
})
test('progress labels have English translations', () => {
  const dictionary = JSON.parse(readFileSync(new URL('../../src/i18n/en.json', import.meta.url), 'utf8'))
  for (const match of source.matchAll(/'([^'\n]*[\u3400-\u9fff][^'\n]*)'/g)) assert.ok(dictionary[match[1]], `missing translation: ${match[1]}`)
})

test('mixed cloud results belong to individual targets, not the aggregate status', () => {
 const cloud={releaseRunId:'r',state:'failed',targets:[{targetId:'pc',state:'succeeded',summary:''},{targetId:'android',state:'failed',summary:'Signing: missing key'},{targetId:'web',state:'running',summary:''}]}
 for(const [id,want] of [['pc','succeeded'],['android','failed'],['web','running']]) {
   const result=targetProgress({...target,targetId:id,releaseRunId:'r',status:'handed_off'},[],[],cloud)
   assert.equal(result.state,want)
   if(id==='android')assert.match(result.error,/missing key/)
 }
 const unrelated=targetProgress({...target,targetId:'pc',releaseRunId:'other',status:'handed_off'},[],[],cloud)
 assert.equal(unrelated.state,'waiting')
 const offline=targetProgress({...target,targetId:'web',releaseRunId:'r',status:'handed_off'},[],[],{...cloud,state:'unavailable'})
 assert.equal(offline.state,'waiting');assert.match(offline.label,/待确认/)
})

test('saved delivery group survives removed or changed project settings', () => {
 const row={...target,versionGroup:'android'}
 assert.equal(targetProgress(row,[{groupId:'android',state:'published'}],[{id:row.targetId,versionGroup:'wrong'}]).state,'succeeded')
 assert.equal(targetProgress(row,[{groupId:'android',state:'failed',errorMessage:'upload interrupted'}],[]).error,'upload interrupted')
 const replaced=releaseProgress({run:{...run,status:'succeeded'},cloudHandoff:true,cloudBuild:{state:'superseded'}})
 assert.equal(replaced.state,'skipped');assert.equal(replaced.steps.find(step=>step.id==='cloud').state,'skipped')
})
