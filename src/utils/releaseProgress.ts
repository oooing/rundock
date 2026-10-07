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
// Delivery state is authoritative: target rows remain waiting_publish during upload.
export function targetProgress(target: ReleaseTargetRun, deliveries: ReleaseDelivery[], definitions: ReleaseTarget[]) {
  const definition = definitions.find(item => item.id === target.targetId)
  const delivery = target.publish && definition ? deliveries.find(item => item.groupId === definition.versionGroup) : undefined
  if (delivery) return { state: deliveryProgressState(delivery.state), label: deliveryProgressLabels[delivery.state] || delivery.state, error: delivery.errorMessage }
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
  const cloudPending = cloudHandoff && !['succeeded', 'superseded', 'failed'].includes(cloudBuild?.state || '')
  const finished = run.status === 'succeeded' && !cloudPending && !failed && (!hasDelivery || allPublished || localOnly)
  const state: ProgressState = cancelled ? 'cancelled' : failed ? 'failed' : finished ? 'succeeded' : 'running'
  const currentDelivery = deliveries.find(item => ['creating_draft', 'uploading', 'publishing', 'failed'].includes(item.state))
  const definitions: Array<[string, string]> = [['prepare', '准备与检查']]
  if (hasBuild) definitions.push(['build', '构建与打包'], ['verify', '校验并保存产物'])
  if (run.pushRemote && !localOnly) definitions.push(['push', run.createTag ? '推送代码与 Tag' : '推送代码'])
  if (!localOnly && selections.some(item => item.deploy || (item.publish && !hasDelivery))) definitions.push(['execute', '执行发布或部署'])
  if (hasDelivery && !localOnly) definitions.push(['upload', '上传并核验 GitHub 附件'], ['release', '正式发布 Release'])
  if (cloudHandoff) definitions.push(['cloud', 'GitHub 云端构建与发布'])
  definitions.push(['complete', localOnly || !run.pushRemote ? '本地完成' : hasDelivery ? '发布完成' : '流程完成'])
  let phase = 'prepare'
  const stage = run.stage
  if (['building_targets', 'target_build', 'target_package', 'target_check', 'local_check', 'local_build', 'local_package'].includes(stage)) phase = 'build'
  if (['local_verifying', 'local_saving', 'delivery_preparing', 'delivery_preflight'].includes(stage)) phase = 'verify'
  if (['tagging', 'pushing_branch', 'pushing_tag'].includes(stage)) phase = run.pushRemote ? 'push' : hasBuild ? 'verify' : 'prepare'
  if (['publishing_targets', 'target_publish', 'target_deploy'].includes(stage)) phase = hasDelivery ? 'upload' : 'execute'
  if (stage === 'delivery_publish') phase = currentDelivery?.state === 'publishing' ? 'release' : 'upload'
  if (run.status === 'succeeded') phase = cloudHandoff && cloudBuild?.state !== 'succeeded' && cloudBuild?.state !== 'superseded' ? 'cloud'
    : hasDelivery && !allPublished && !localOnly ? currentDelivery?.state === 'publishing' ? 'release' : 'upload' : 'complete'
  let index = definitions.findIndex(([id]) => id === phase)
  if (index < 0) index = 0
  const steps: ProgressStep[] = definitions.map(([id, label], position) => ({ id, label,
    state: finished || position < index ? 'succeeded' : position > index ? 'waiting' : state,
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
  if (cancelled) title = '本次操作已取消'
  const next = state === 'running' ? steps.slice(index + 1).find(item => item.state === 'waiting')?.label : undefined
  return { state, title, phaseLabel: steps[index]?.label || '执行', steps, next, currentDelivery, activeTarget, published: deliveries.filter(item => item.state === 'published').length,
    error: currentDelivery?.errorMessage || targets.find(item => item.status === 'failed')?.errorMessage || run.errorMessage || (cloudBuild?.state === 'failed' ? cloudBuild.summary : ''),
    syncPending: allPublished && deliveries.some(item => ['pending', 'deploying', 'deployment_requested', 'deployment_failed', 'deployment_rejected', 'unverified'].includes(item.syncState)),
  }
}
