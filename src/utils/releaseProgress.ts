import type { CloudBuildStatus, ReleaseRun, ReleaseTarget } from '@/types'
import type { ReleaseDelivery, ReleaseTargetRun } from '@/types/releaseExecution'

export type ProgressState = 'waiting' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'skipped'
export interface ProgressRun extends Omit<ReleaseRun, 'status' | 'stage'> { status: string; stage: string }
export interface ProgressStep { id: string; label: string; state: ProgressState }
export const progressLabels: Record<ProgressState, string> = {
  waiting: '等待', running: '进行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', skipped: '已跳过',
}
export function formatArtifactSize(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let size = value, unit = 0
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit++ }
  return `${unit ? Number(size.toFixed(1)) : size} ${units[unit]}`
}
export function artifactSizeSummary(items: Array<{ sizeBytes?: number }>) {
  const known = items.filter(item => typeof item.sizeBytes === 'number' && Number.isFinite(item.sizeBytes) && item.sizeBytes >= 0)
  return { size: known.length ? formatArtifactSize(known.reduce((sum, item) => sum + item.sizeBytes!, 0)) : '—', missing: items.length - known.length }
}
export const deliveryProgressLabels: Record<string, string> = {
  sealed: '等待上传 GitHub', creating_draft: '正在创建 GitHub Release 草稿', uploading: '正在上传 GitHub Release',
  publishing: '正在确认 Release 发布', published: 'GitHub Release 已发布', failed: 'GitHub 上传或发布失败',
}
export function deliveryProgressState(state: string): ProgressState {
  return state === 'published' ? 'succeeded' : state === 'failed' ? 'failed'
    : ['creating_draft', 'uploading', 'publishing'].includes(state) ? 'running' : 'waiting'
}
// Build completion is independent of delivery: a sealed file can be waiting
// for upload without turning a successful build back into a waiting build.
export function targetBuildProgress(target: ReleaseTargetRun): { state: ProgressState; label: string } {
  if ((!target.build && !target.package) || ['handed_off', 'triggered', 'remote_pending'].includes(target.status)) return { state: 'skipped', label: '无需本机生成' }
  if ((!target.build || target.buildDone) && (!target.package || target.packageDone)) return { state: 'succeeded', label: '生成成功' }
  if (target.status === 'failed') return { state: 'failed', label: '生成未完成' }
  if (target.status === 'running') return { state: 'running', label: target.stage === 'package' ? '打包中' : '生成中' }
  return { state: 'waiting', label: '等待生成' }
}
const networkMessages: Record<string, string> = {
  github_connection_interrupted: '读取 GitHub 信息时连接中断，不代表发布配置失效。',
  github_connection_timeout: '连接 GitHub 超时，请检查网络或代理。',
  github_dns_failed: '找不到 GitHub 服务器，请检查网络或代理。',
  github_connection_failed: '无法连接 GitHub 或代理，请检查连接。',
  github_proxy_unavailable: 'GitHub 代理暂不可用，请检查代理后重试。',
  github_request_unconfirmed: '尚未确认 GitHub 连接，请稍后重试。未证实登录失效。',
}
export function savedDeliveryReady(run: ProgressRun, deliveries: ReleaseDelivery[], targets: ReleaseTargetRun[]) {
  return !!run.commitSha && deliveries.length > 0 && deliveries.every(item => !!item.manifestSha256 && (!item.localArtifactsState || item.localArtifactsState === 'retained'))
    && !run.selectedTargets.some(item => item.deploy)
    && run.selectedTargets.filter(item => item.build || item.package).every(choice => {
      const target = targets.find(item => item.targetId === choice.targetId)
      return !!target && (!choice.build || target.buildDone) && (!choice.package || target.packageDone)
    })
}
export function targetUploadProgress(target: ReleaseTargetRun, deliveries: ReleaseDelivery[], definitions: ReleaseTarget[], cloudBuild?: CloudBuildStatus | null) {
  if ((target.build || target.package) && !['succeeded', 'skipped'].includes(targetBuildProgress(target).state))
    return { state: 'waiting' as ProgressState, label: '等待生成完成', error: '' }
  return targetProgress(target, deliveries, definitions, cloudBuild)
}
// Delivery state is authoritative: target rows remain waiting_publish during upload.
export function targetProgress(target: ReleaseTargetRun, deliveries: ReleaseDelivery[], definitions: ReleaseTarget[], cloudBuild?: CloudBuildStatus | null) {
  const definition = definitions.find(item => item.id === target.targetId)
  const group = target.versionGroup || definition?.versionGroup
  const delivery = target.publish && group ? deliveries.find(item => item.groupId === group)
    || (definition?.versionGroup === group && deliveries.find(item => item.groupId === definition.delivery?.releaseGroup)) : undefined
  if (delivery) return { state: deliveryProgressState(delivery.state), label: deliveryProgressLabels[delivery.state] || delivery.state, error: delivery.errorMessage }
  const cloudTarget = target.publish && ['handed_off','remote_pending','triggered'].includes(target.status) && target.releaseRunId === cloudBuild?.releaseRunId ? cloudBuild?.targets?.find(item => item.targetId === target.targetId) : undefined
  if (cloudTarget) {
    if (cloudBuild?.state === 'unavailable' && !['succeeded','failed'].includes(cloudTarget.state))
      return {state:'waiting' as ProgressState,label:'连接中断，结果待确认',error:''}
    const labels: Record<string,string> = {succeeded:'云端构建已完成',failed:'云端执行失败',running:'云端执行中',pending:'等待云端结果',superseded:'已有新版本，此版本未完成'}
    const state: ProgressState = cloudTarget.state === 'succeeded' ? 'succeeded' : cloudTarget.state === 'failed' ? 'failed' : cloudTarget.state === 'running' ? 'running' : cloudTarget.state === 'superseded' ? 'skipped' : 'waiting'
    return {state,label:labels[cloudTarget.state] || '结果待确认',error:cloudTarget.summary}
  }
  // Handoff records describe local dispatch, not the eventual cloud result.
  // Aggregate success proves all matched workflows succeeded; aggregate failure
  // or supersession cannot identify an individual target's outcome.
  if (target.publish && ['handed_off', 'remote_pending', 'triggered'].includes(target.status)
    && target.releaseRunId && cloudBuild?.releaseRunId === target.releaseRunId && cloudBuild.state === 'succeeded') {
    return { state: 'succeeded' as ProgressState, label: '云端构建已完成', error: '' }
  }
  const state: ProgressState = ['running', 'succeeded', 'failed', 'cancelled', 'skipped'].includes(target.status)
    ? target.status as ProgressState : 'waiting'
  const labels: Record<string, string> = { build: '构建', package: '打包', check: '检查', checking: '检查', artifacts: '核对产物', publish: '上传', deploy: '部署', waiting_publish: '等待上传或部署', ready_to_publish: '等待上传或部署', triggered: '等待云端处理', cloud_pending: '云端结果待确认', completed: '已完成' }
  return { state, label: labels[target.stage] || progressLabels[state], error: target.errorMessage }
}

export function releaseProgress(input: {
  run: ProgressRun; targets?: ReleaseTargetRun[]; deliveries?: ReleaseDelivery[];
  deliveryPlanned?: boolean; cloudHandoff?: boolean; cloudBuild?: CloudBuildStatus | null; localOnly?: boolean;
}) {
  const { run, targets = [], deliveries = [], cloudHandoff = false, cloudBuild, localOnly = false } = input
  const selections = run.selectedTargets || []
  const hasBuild = localOnly || selections.some(item => item.build || item.package)
  const hasDelivery = deliveries.length > 0 || !!input.deliveryPlanned
  const allPublished = deliveries.length > 0 && deliveries.every(item => item.state === 'published')
  const failed = run.status === 'failed' || cloudBuild?.state === 'failed'
  const cancelled = run.status === 'cancelled' || /cancelled/.test(run.errorCode || '')
  const superseded = cloudHandoff && cloudBuild?.state === 'superseded'
  const cloudPending = cloudHandoff && !['succeeded', 'superseded', 'failed'].includes(cloudBuild?.state || '')
  const finished = run.status === 'succeeded' && !cloudPending && !failed && (!hasDelivery || allPublished || localOnly)
  const state: ProgressState = cancelled ? 'cancelled' : failed ? 'failed' : superseded ? 'skipped' : finished ? 'succeeded' : 'running'
  const currentDelivery = deliveries.find(item => ['creating_draft', 'uploading', 'publishing', 'failed'].includes(item.state))
  const definitions: Array<[string, string]> = [['prepare', '准备与检查']]
  if (hasBuild) definitions.push(['build', '构建与打包'], ['verify', '校验并保存产物'])
  if (hasDelivery && !localOnly) definitions.push(['connect', '连接 GitHub'])
  if (run.pushRemote && !localOnly) definitions.push(['push', run.createTag ? '推送代码与 Tag' : '推送代码'])
  if (!localOnly && selections.some(item => item.deploy || (item.publish && !hasDelivery))) definitions.push(['execute', '执行发布或部署'])
  if (hasDelivery && !localOnly) definitions.push(['upload', '上传并核验 GitHub 附件'], ['release', '正式发布 Release'])
  if (cloudHandoff) definitions.push(['cloud', 'GitHub 云端构建与发布'])
  definitions.push(['complete', localOnly || !run.pushRemote ? '本地完成' : hasDelivery ? '发布完成' : '流程完成'])
  let phase = 'prepare'
  const stage = run.stage
  // Older records used delivery_preflight for both the pre-build connection
  // check and the post-build verification. No commit means construction never began.
  const connecting = stage === 'delivery_connecting' || stage === 'delivery_preflight' && !run.commitSha
  if (['building_targets', 'target_build', 'target_package', 'target_check', 'local_check', 'local_build', 'local_package'].includes(stage)) phase = 'build'
  if (['local_verifying', 'local_saving', 'delivery_preparing', 'delivery_preflight'].includes(stage)) phase = 'verify'
  // The same backend stage also verifies saved files. Only a known GitHub
  // failure proves it reached the remote check; file/hash failures stay here.
  if (stage === 'delivery_preflight' && run.commitSha && run.errorCode?.startsWith('github_')) phase = 'connect'
  if (connecting) { phase = 'prepare'; definitions[0][1] = '连接 GitHub' }
  if (['tagging', 'pushing_branch', 'pushing_tag'].includes(stage)) phase = run.pushRemote ? 'push' : hasBuild ? 'verify' : 'prepare'
  if (['publishing_targets', 'target_publish', 'target_deploy'].includes(stage)) phase = hasDelivery ? 'upload' : 'execute'
  if (stage === 'delivery_publish') phase = currentDelivery?.state === 'publishing' ? 'release' : 'upload'
  if (run.status === 'succeeded') phase = cloudHandoff && cloudBuild?.state !== 'succeeded' && cloudBuild?.state !== 'superseded' ? 'cloud'
    : hasDelivery && !allPublished && !localOnly ? currentDelivery?.state === 'publishing' ? 'release' : 'upload' : 'complete'
  let index = definitions.findIndex(([id]) => id === phase)
  if (index < 0) index = 0
  const steps: ProgressStep[] = definitions.map(([id, label], position) => ({ id, label,
    state: superseded && ['cloud','complete'].includes(id) ? 'skipped' : finished || position < index ? 'succeeded' : position > index ? 'waiting' : state,
  }))
  // Groups upload/publish sequentially, so a publishing group does not prove
  // that the other groups' assets are uploaded. Never mark that aggregate done.
  if (['upload', 'release'].includes(phase) && deliveries.length) {
    const upload = steps.find(item => item.id === 'upload')
    const release = steps.find(item => item.id === 'release')
    if (upload) upload.state = deliveries.every(item => ['publishing', 'published'].includes(item.state)) ? 'succeeded' : phase === 'upload' ? state : 'waiting'
    if (release) release.state = allPublished ? 'succeeded' : phase === 'release' ? state : 'waiting'
  }
  const activeTarget = targets.find(item => item.status === 'running')
  let title = steps[index]?.label || '正在执行'
  if (currentDelivery && stage === 'delivery_publish') title = deliveryProgressLabels[currentDelivery.state] || title
  else if (activeTarget && phase === 'build') title = activeTarget.stage === 'package' ? '正在打包' : '正在构建'
  else if (phase === 'push') title = stage === 'pushing_tag' ? '正在推送 Tag' : '正在推送代码'
  if (finished) title = allPublished ? '构建与发布已完成' : cloudHandoff ? '云端构建已完成' : localOnly ? '本地构建完成' : '本次操作已完成'
  if (cloudPending) title = cloudBuild?.state === 'running' ? '云端构建进行中' : '等待云端结果'
  if (cloudBuild?.state === 'superseded') title = '旧版本已由新构建替代'
  if (failed) title = '步骤失败：{0}'
  const networkError = failed && networkMessages[run.errorCode || '']
  if (networkError) title = 'GitHub 连接失败'
  if (cancelled) title = '本次操作已取消'
  const next = state === 'running' ? steps.slice(index + 1).find(item => item.state === 'waiting')?.label : undefined
  return { state, title, phaseLabel: steps[index]?.label || '执行', steps, next, currentDelivery, activeTarget, published: deliveries.filter(item => item.state === 'published').length,
    recovery: networkError ? savedDeliveryReady(run, deliveries, targets)
      ? '文件已保存。网络恢复后重试上传，无需重新生成。'
      : '检查网络或代理，恢复连接后再重试。' : '',
    error: stage === 'delivery_preflight' && !run.commitSha && run.errorCode === 'github_request_unconfirmed'
      ? '读取 GitHub 信息失败；旧记录未保存具体连接原因，请返回发布重试。'
      : networkError || currentDelivery?.errorMessage || targets.find(item => item.status === 'failed')?.errorMessage || run.errorMessage || (cloudBuild?.state === 'failed' ? cloudBuild.summary : ''),
    syncPending: allPublished && deliveries.some(item => ['pending', 'deploying', 'deployment_requested', 'deployment_failed', 'deployment_rejected', 'unverified'].includes(item.syncState)),
  }
}
