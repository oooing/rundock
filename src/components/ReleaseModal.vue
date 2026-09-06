<script setup lang="ts">
import { tr } from '@/i18n'

import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '@/api/http'
import { readReleaseSession, rememberReleaseSession } from '@/utils/releaseSession'
import { releaseContentState } from '@/utils/releaseContent'
import type {
  AppView,
  ReleaseConfig,
  ReleaseLog,
  ReleasePreflight,
  ReleaseRun,
  ReleaseArtifact,
  ReleaseAutomationStatus,
  ReleaseFileChange,
  ReleaseTarget,
  ReleaseTargetRun,
  ReleaseTargetSteps,
  ReleaseVersionGroup,
  ReleaseVersionMode,
  SelectedReleaseTarget,
  VersionStrategy,
} from '@/types'

const props = defineProps<{ app: AppView }>()
const emit = defineEmits<{ (e: 'close'): void }>()

type ExecutionPhase = 'build' | 'package' | 'publish' | 'deploy'
type TargetChoice = { selected: boolean } & Record<ExecutionPhase, boolean>
type ProductPlatformId = 'web' | 'pc' | 'android' | 'mac' | 'server' | `custom:${string}`
type ProductPlatform = {
  id: ProductPlatformId
  name: string
  icon: string
  description: string
  targets: ReleaseTarget[]
  configured: boolean
}

const phaseOptions = computed<Array<{ key: ExecutionPhase; label: string; hint: string; risky: boolean }>>(() => ([
  { key: 'build', label: tr("构建"), hint: tr("生成可运行代码"), risky: false },
  { key: 'package', label: tr("打包"), hint: tr("生成安装包或压缩包"), risky: false },
  { key: 'publish', label: tr("上传"), hint: tr("上传到发布平台"), risky: true },
  { key: 'deploy', label: tr("部署上线"), hint: tr("让线上用户看到新版"), risky: true },
]))

const loading = ref(true)
const checkingRemote = ref(false)
const savingProfile = ref(false)
const publishing = ref(false)
const error = ref('')
const preflight = ref<ReleasePreflight | null>(null)
const history = ref<ReleaseRun[]>([])
const selected = ref<Record<string, boolean>>({})
const versionInputs = ref<Record<string, string>>({})
const commitMessage = ref('')
const commitMessageDirty = ref(false)
const releaseNotes = ref('')
const releaseNotesDirty = ref(false)
const releaseNotesStale = ref(false)
const releaseNotesLoading = ref(false)
const releaseNotesError = ref('')
const releaseNotesBaseTag = ref('')
const releaseNotesSourceFingerprint = ref('')
const releaseNotesGeneratedFor = ref('')
const remoteName = ref('origin')
const versionStrategy = ref<VersionStrategy>('auto')
const preReleaseCommand = ref('')
const createTag = ref(true)
const pushRemote = ref(true)
const versionMode = ref<ReleaseVersionMode>('auto')
const profileReady = ref(false)

const releaseConfig = ref<ReleaseConfig | null>(null)
const configDraft = ref<ReleaseConfig | null>(null)
const configBeforeEdit = ref<ReleaseConfig | null>(null)
const targetChoices = ref<Record<string, TargetChoice>>({})
const configEndpointAvailable = ref(true)
const configNotice = ref('')
const configEditorOpen = ref(false)
const configScanning = ref(false)
const configSaving = ref(false)
const configValidationError = ref('')
const preflightStale = ref(false)
const gitOnly = ref(false)
const advancedOpen = ref(false)
const confirmAction = ref<'retry' | 'regenerate-notes' | null>(null)
const pushMenuOpen = ref(false)
const publishControlRef = ref<HTMLElement | null>(null)
const bodyRef = ref<HTMLElement | null>(null)

const activeRun = ref<ReleaseRun | null>(null)
const logs = ref<ReleaseLog[]>([])
const runTargets = ref<ReleaseTargetRun[]>([])
const runArtifacts = ref<ReleaseArtifact[]>([])
const runAutomation = ref<ReleaseAutomationStatus | null>(null)
let pollTimer: ReturnType<typeof setTimeout> | null = null
let preferenceTimer: ReturnType<typeof setTimeout> | null = null
let releaseNotesTimer: ReturnType<typeof setTimeout> | null = null
let releaseNotesRequest = 0
let preferenceSave = Promise.resolve()
let disposed = false

const isActive = computed(() => ['starting', 'running', 'degraded', 'stopping'].includes(props.app.status))
const selectedPaths = computed(() => Object.entries(selected.value).filter(([, value]) => value).map(([path]) => path))
const orderedChanges = computed(() => {
  const changes = preflight.value?.changes || []
  return [...changes.filter(isAddedFile), ...changes.filter((file) => !isAddedFile(file))]
})
const newFiles = computed(() => preflight.value?.changes.filter(isAddedFile) || [])
const unselectedNewFiles = computed(() => newFiles.value.filter((file) => !selected.value[file.path]))
const allFilesSelected = computed(() => !!preflight.value?.changes.length && preflight.value.changes.every((file) => selected.value[file.path]))
const configuredTargets = computed(() => releaseConfig.value?.targets || [])
const chosenTargets = computed(() => configuredTargets.value
  .filter((target) => targetChoices.value[target.id]?.selected && targetAvailable(target))
  .map((target) => ({ target, choice: targetChoices.value[target.id] })))
const selectedTargets = computed<SelectedReleaseTarget[]>(() => gitOnly.value ? [] : chosenTargets.value
  .map(({ target, choice }) => {
    return {
      targetId: target.id,
      build: !!target.steps.build && !!choice.build,
      package: !!target.steps.package && !!choice.package,
      publish: !!target.steps.publish && !!choice.publish,
      deploy: !!target.steps.deploy && !!choice.deploy,
    }
  }))
const invalidChosenTargetIds = computed(() => selectedTargets.value
  .filter((target) => !target.build && !target.package && !target.publish && !target.deploy)
  .map((target) => target.targetId))
const selectedVersionGroupIds = computed(() => [...new Set(chosenTargets.value.map(({ target }) => target.versionGroup))])
const selectedVersionGroups = computed(() => (releaseConfig.value?.versionGroups || [])
  .filter((group) => selectedVersionGroupIds.value.includes(group.id)))
const selectedVersionFiles = computed(() => {
  if (gitOnly.value || !selectedVersionGroups.value.length) return preflight.value?.versionFiles || []
  return [...new Set(selectedVersionGroups.value.flatMap((group) => group.versionFiles.map((file) => file.path)))]
})
const visibleCurrentVersions = computed<Record<string, string>>(() => {
  const values: Record<string, string> = {}
  if (gitOnly.value || !selectedVersionGroups.value.length) {
    for (const path of preflight.value?.versionFiles || []) values[path] = preflight.value?.currentVersions[path] || ''
    return values
  }
  for (const group of selectedVersionGroups.value) {
    if (!group.versionFiles.length && group.currentVersion) values[group.name] = group.currentVersion
    for (const file of group.versionFiles) values[file.path] = preflight.value?.currentVersions[file.path] || group.currentVersion || ''
  }
  return values
})
function isTagPushTarget(target: ReleaseTarget) {
  return target.runner.type.trim().toLowerCase() === 'git-push'
    && (target.steps.publish || '').trim().toLowerCase() === 'tag-push'
}

function canResumeFailedRun(run: ReleaseRun) {
  if (!canRetryRun(run)) return false
  const oldLocalStage = ['building_targets', 'target_check', 'target_build', 'target_package'].includes(run.stage)
  if (!oldLocalStage) return true
  return !run.selectedTargets.some((selection) => {
    if (!selection.build && !selection.package) return false
    const currentTarget = configuredTargets.value.find((target) => target.id === selection.targetId)
    return !!currentTarget && isTagPushTarget(currentTarget)
  })
}
type PlannedVersion = { versionGroupId: string; versionGroupName: string; currentVersion: string; suggestedVersion: string; targetVersion: string; tagName: string }
const plannedVersions = computed<PlannedVersion[]>(() => {
  const pf = preflight.value
  if (!pf || !createTag.value) return []
  if (gitOnly.value || !selectedVersionGroups.value.length) {
    const suggestedVersion = pf.suggestedVersion || '0.1.0'
    const target = versionMode.value === 'auto' ? suggestedVersion : (versionInputs.value.repository || suggestedVersion)
    return [{ versionGroupId: 'repository', versionGroupName: tr("项目版本"), currentVersion: pf.latestTag || tr("未创建 Tag"), suggestedVersion, targetVersion: target, tagName: `v${target}` }]
  }
  const namespaced = (releaseConfig.value?.versionGroups.length || 0) > 1
  return selectedVersionGroups.value.map((group) => {
    const values: string[] = []
    if (!group.versionFiles.length && group.currentVersion) values.push(group.currentVersion)
    for (const file of group.versionFiles) values.push(pf.currentVersions[file.path] || group.currentVersion || '')
    const latestGroupVersion = (pf.latestGroupTags[group.id] || '').replace(/^.*\/v/, '')
    const suggestedVersion = namespaced
      ? (pf.suggestedVersions[group.id] || nextPatchVersion([...values, latestGroupVersion]))
      : suggestReleaseVersion(values.filter(Boolean), pf.latestTag)
    const target = versionMode.value === 'auto' ? suggestedVersion : (versionInputs.value[group.id] || suggestedVersion)
    const prefix = group.tagPrefix || group.id
    return {
      versionGroupId: group.id,
      versionGroupName: versionGroupDisplayName(group),
      currentVersion: latestGroupVersion || values.find((value) => /^(?:v)?\d+\.\d+\.\d+$/.test(value))?.replace(/^v/, '') || tr("未识别"),
      suggestedVersion,
      targetVersion: target,
      tagName: namespaced ? `${prefix}/v${target}` : `v${target}`,
    }
  })
})
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
const versionValid = computed(() => !createTag.value || (plannedVersions.value.length > 0 && plannedVersions.value.every((version) => versionPattern.test(version.targetVersion))))
const primaryTargetVersion = computed(() => plannedVersions.value[0]?.targetVersion || '')
const plannedTagNames = computed(() => plannedVersions.value.map((version) => version.tagName))
const configNeedsSaving = computed(() => !!releaseConfig.value?.targets.length && releaseConfig.value.source !== 'file')
const selectedNeedsRemotePush = computed(() => chosenTargets.value.some(({ target }) => target.runner.type.trim().toLowerCase() === 'git-push'))
const configuredAutomation = computed(() => {
  const automation = releaseConfig.value?.automation
  return automation?.provider.trim().toLowerCase() === 'github-actions' ? automation : null
})
const selectedHasTagPushTarget = computed(() => chosenTargets.value.some(({ target }) => isTagPushTarget(target)))
const automationTargetRequiresTag = computed(() => selectedHasTagPushTarget.value
  || (!!configuredAutomation.value
    && configuredAutomation.value.trigger.trim().toLowerCase() === 'tag'
    && selectedNeedsRemotePush.value))
const willTriggerAutomation = computed(() => (selectedHasTagPushTarget.value
  || (!!configuredAutomation.value
    && configuredAutomation.value.trigger.trim().toLowerCase() === 'tag'
    && configuredAutomation.value.publishesRelease))
  && createTag.value
  && pushRemote.value)
const automationBranchMismatch = computed(() => !!willTriggerAutomation.value
  && !!configuredAutomation.value?.releaseBranch
  && configuredAutomation.value.releaseBranch !== preflight.value?.branch)
const willBuildWindowsInAutomation = computed(() => productPlatforms.value.some((platform) => platform.id === 'pc' && platformHasSelection(platform)))
const willBuildTargetsInAutomation = computed(() => selectedHasTagPushTarget.value || willBuildWindowsInAutomation.value)
const releaseNotesOptionsSignature = computed(() => JSON.stringify({
  statusFingerprint: preflight.value?.statusFingerprint || '',
  selectedPaths: [...selectedPaths.value].sort(),
  selectedTargets: [...selectedTargets.value].sort((left, right) => left.targetId.localeCompare(right.targetId)),
  createTag: createTag.value,
  versions: plannedVersions.value.map((version) => `${version.versionGroupId}:${version.tagName}`),
}))
const targetSelectionValid = computed(() => (gitOnly.value
  || (selectedTargets.value.length > 0 && invalidChosenTargetIds.value.length === 0))
  && (pushRemote.value || !selectedNeedsRemotePush.value)
  && (createTag.value || !automationTargetRequiresTag.value)
  && !automationBranchMismatch.value)
const newContentState = computed(() => {
  const pf = preflight.value
  if (!pf) return 'unknown'
  const namespaced = (releaseConfig.value?.versionGroups.length || 0) > 1
  const baseTags = plannedVersions.value.map(version => namespaced && version.versionGroupId !== 'repository'
    ? pf.latestGroupTags[version.versionGroupId] || '' : pf.latestTag || '')
  return releaseContentState(createTag.value, selectedPaths.value, pf.changes.map(change => change.path), baseTags, pf.commitsSinceTags)
})
const releaseContentHint = computed(() => newContentState.value === 'none'
  ? tr('暂无新内容，无需发布新版本')
  : newContentState.value === 'unknown' && preflight.value?.remoteChecked && !checkingRemote.value
    ? tr('无法确认版本后的改动，请刷新发布检查；旧版后端需先更新') : '')
const canPublish = computed(() => {
  if (!preflight.value?.canRelease || !preflight.value.remoteChecked || checkingRemote.value || preflightStale.value || activeRun.value || publishing.value || !commitMessage.value.trim()) return false
  if (createTag.value && (!versionValid.value || releaseNotesLoading.value || !releaseNotes.value.trim())) return false
  if (newContentState.value !== 'new') return false
  return targetSelectionValid.value
})
function canRetryRun(run: ReleaseRun | null | undefined) {
  return !!run && run.status === 'failed' && !!run.commitSha && run.errorCode !== 'build_changed_tree' && [
    'tagging', 'pushing_branch', 'pushing_tag', 'building_targets', 'publishing_targets',
    'target_build', 'target_package', 'target_publish', 'target_deploy',
  ].includes(run.stage)
}
const retryable = computed(() => canRetryRun(activeRun.value))
const externalRetryRisk = computed(() => !!activeRun.value && (
  activeRun.value.pushRemote
  || activeRun.value.selectedTargets.some((target) => target.publish || target.deploy)
))
const hasOnlineAction = computed(() => selectedTargets.value.some((target) => target.publish || target.deploy))
const hasExternalAction = computed(() => hasOnlineAction.value || willTriggerAutomation.value)
const remoteDestination = computed(() => /github\.com/i.test(preflight.value?.remoteUrl || '') ? 'GitHub' : tr("远程仓库"))
const automationPageUrl = computed(() => runAutomation.value?.url
  || activeRun.value?.automationUrl
  || githubActionsUrl(preflight.value?.remoteUrl || '', configuredAutomation.value?.workflow || ''))
const automationHandedOff = computed(() => !!activeRun.value?.pushRemote
  && !!activeRun.value?.createTag
  && !!(runAutomation.value || activeRun.value.automationUrl || configuredAutomation.value))
const activeRunStatusLabel = computed(() => {
  if (activeRun.value?.status === 'failed') return tr("失败")
  if (activeRun.value?.status !== 'succeeded') return tr("进行中")
  return automationHandedOff.value ? tr("已提交 GitHub") : tr("成功")
})
const activeStageLabel = computed(() => {
  if (!activeRun.value) return ''
  if (activeRun.value.stage === 'completed' && automationHandedOff.value) return tr("本地发布完成，等待 GitHub 自动处理")
  return stageLabel.value[activeRun.value.stage] || activeRun.value.stage
})
const cloudExecutionNotice = computed(() => {
  if (activeRun.value ? !automationHandedOff.value : !willTriggerAutomation.value) return null
  const selections = activeRun.value?.selectedTargets || selectedTargets.value
  if (!selections.length) return {
    title: tr("GitHub Actions 发布源码版本"),
    text: tr("代码和 Tag 上传后，由 GitHub Actions 创建源码版本，不生成安装包。"),
  }
  const allCloud = selections.every((selection) => configuredTargets.value
    .find((target) => target.id === selection.targetId)?.runner.type.trim().toLowerCase() === 'git-push')
  return {
    title: tr("GitHub Actions 云端构建与发布"),
    text: allCloud
      ? tr("本机不构建、不打包。构建、打包和发布由 GitHub Actions 按项目配置执行。")
      : tr("云端目标由 GitHub Actions 构建、打包并按项目配置发布；本地目标仍按配置执行。"),
  }
})
const completionTitle = computed(() => activeRun.value?.pushRemote
  ? tr("已提交到 {0}", [automationHandedOff.value ? 'GitHub' : remoteDestination.value])
  : tr("本地操作已完成"))
const completionDescription = computed(() => {
  if (!activeRun.value?.pushRemote) return tr("提交已保存在本机，尚未上传到远程仓库。")
  return activeRun.value.createTag ? tr("代码和版本 Tag 已上传。") : tr("代码已上传。")
})
const confirmDialogTitle = computed(() => {
  if (confirmAction.value === 'regenerate-notes') return tr("重新生成更新说明？")
  return tr("确认重试远端操作？")
})
const confirmDialogMessage = computed(() => {
  if (confirmAction.value === 'regenerate-notes') return tr("重新生成会覆盖你手动修改的内容。")
  return tr("请先确认远端没有成功；继续可能造成重复上传或重复上线。")
})
const confirmDialogButton = computed(() => confirmAction.value === 'regenerate-notes' ? tr("覆盖并生成") : tr("继续重试"))
const configConfidence = computed(() => Math.round((releaseConfig.value?.confidence || 0) * 100))
const standardPlatforms = computed<Array<{ id: Exclude<ProductPlatformId, `custom:${string}`>; name: string; icon: string; description: string }>>(() => ([
  { id: 'web', name: tr("Web 前端"), icon: '🌐', description: tr("网页界面") },
  { id: 'pc', name: 'PC', icon: '🖥️', description: tr("Windows 桌面端") },
  { id: 'android', name: 'Android', icon: '🤖', description: tr("Android 应用") },
  { id: 'mac', name: 'Mac', icon: '🍎', description: tr("macOS 桌面端") },
  { id: 'server', name: tr("后端服务"), icon: '🗄️', description: tr("API / 后台任务") },
]))

function platformIdForTarget(target: ReleaseTarget): ProductPlatformId | null {
  const kind = target.kind.trim().toLowerCase()
  const clue = `${target.id} ${target.name}`.toLowerCase()
  if (kind === 'node') return null
  if (kind === 'desktop') {
    const systems = target.runner.os.map((value) => value.trim().toLowerCase()).filter(Boolean)
    if (systems.length === 1 && systems[0] === 'darwin') return 'mac'
    if (systems.length === 1 && systems[0] === 'windows') return 'pc'
    if (!systems.length) {
      if (clue.includes('macos') || clue.includes('mac ')) return 'mac'
      if (clue.includes('windows')) return 'pc'
    }
    return `custom:${target.id}`
  }
  if (kind === 'web') return 'web'
  if (kind === 'android') return 'android'
  if (['server', 'service', 'backend', 'docker'].includes(kind)) return 'server'
  if (['mac', 'macos', 'darwin'].includes(kind) || clue.includes('macos') || clue.includes('mac ')) return 'mac'
  if (['windows', 'pc'].includes(kind) || clue.includes('windows')) return 'pc'
  return `custom:${target.id}`
}

function versionGroupDisplayName(group: ReleaseVersionGroup) {
  if (group.name && !/^版本\s+\d+\.\d+\.\d+$/.test(group.name) && group.name !== '产品版本') return group.name
  const platformIds = new Set(configuredTargets.value
    .filter((target) => target.versionGroup === group.id)
    .map(platformIdForTarget)
    .filter((id): id is ProductPlatformId => !!id))
  const platformNames = standardPlatforms.value.filter((platform) => platformIds.has(platform.id)).map((platform) => platform.name)
  for (const id of platformIds) if (id.startsWith('custom:')) platformNames.push(id.replace(/^custom:/, ''))
  if (platformNames.length > 1) return tr("{0}共用版本", [platformNames.join(' / ')])
  if (platformNames.length === 1) return tr("{0}版本", [platformNames[0]])
  return group.name || tr("项目版本")
}

const productPlatforms = computed<ProductPlatform[]>(() => {
  const grouped = new Map<ProductPlatformId, ReleaseTarget[]>()
  for (const platform of standardPlatforms.value) grouped.set(platform.id, [])
  for (const target of configuredTargets.value) {
    const platformId = platformIdForTarget(target)
    if (!platformId) continue
    const targets = grouped.get(platformId) || []
    targets.push(target)
    grouped.set(platformId, targets)
  }
  const cards: ProductPlatform[] = standardPlatforms.value.map((platform) => ({
    ...platform,
    targets: grouped.get(platform.id) || [],
    configured: !!grouped.get(platform.id)?.length,
  }))
  for (const [id, targets] of grouped) {
    if (!id.startsWith('custom:')) continue
    const target = targets[0]
    const isDesktop = target?.kind.trim().toLowerCase() === 'desktop'
    cards.push({ id, name: isDesktop ? tr("桌面端") : target?.name || tr("自定义目标"), icon: isDesktop ? '💻' : '🧩', description: tr("自定义发布目标"), targets, configured: true })
  }
  return cards
})

function configuredActions(target: ReleaseTarget) {
  return phaseOptions.value.filter((phase) => !!target.steps[phase.key])
}

function platformRunnableTargets(platform: ProductPlatform) {
  return platform.targets.filter((target) => targetAvailable(target) && configuredActions(target).length > 0)
}

function platformSelected(platform: ProductPlatform) {
  const runnable = platformRunnableTargets(platform)
  return runnable.length > 0 && runnable.every((target) => targetChoices.value[target.id]?.selected)
}

function platformHasSelection(platform: ProductPlatform) {
  return platformRunnableTargets(platform).some((target) => targetChoices.value[target.id]?.selected)
}

function platformPartiallySelected(platform: ProductPlatform) {
  const count = platformSelectionCount(platform)
  return count.selected > 0 && count.selected < count.runnable
}

function platformPartiallyAvailable(platform: ProductPlatform) {
  const count = platformSelectionCount(platform)
  return count.runnable > 0 && count.runnable < count.total
}

function platformSelectionCount(platform: ProductPlatform) {
  const runnable = platformRunnableTargets(platform)
  return {
    selected: runnable.filter((target) => targetChoices.value[target.id]?.selected).length,
    runnable: runnable.length,
    total: platform.targets.length,
  }
}

function platformUnavailableReason(platform: ProductPlatform) {
  if (!platform.configured) {
    if (platform.id === 'mac') return currentOS() === 'darwin' ? tr("未配置 Mac 构建") : tr("未配置，且需在 macOS 电脑运行")
    return tr("未识别到此平台")
  }
  const runnable = platformRunnableTargets(platform)
  if (runnable.length) return ''
  if (platform.id === 'mac') {
    const needsMac = platform.targets.some((target) => {
      const systems = target.runner.os.map((value) => value.trim().toLowerCase())
      return target.runner.type.trim().toLowerCase() === 'local' && systems.includes('darwin') && !systems.includes('any')
    })
    if (needsMac && currentOS() !== 'darwin') return tr("需在 macOS 电脑运行")
  }
  const environmentReason = platform.targets.map(targetUnavailableReason).find(Boolean)
  if (environmentReason) return environmentReason
  return tr("尚未配置可执行的构建或发布动作")
}

function platformActionLabels(platform: ProductPlatform, selectedOnly = false) {
  const targetIds = new Set(platform.targets.map((target) => target.id))
  return phaseOptions.value
    .filter((phase) => selectedOnly
      ? selectedTargets.value.some((target) => targetIds.has(target.targetId) && target[phase.key])
      : platformRunnableTargets(platform).some((target) => !!target.steps[phase.key]))
    .map((phase) => phase.label)
}

function platformCardDetail(platform: ProductPlatform) {
  const parts = [platform.description]
  const unavailableReason = platformUnavailableReason(platform)
  if (unavailableReason) return `${platform.description} · ${unavailableReason}`

  if (platformPartiallySelected(platform)) {
    const count = platformSelectionCount(platform)
    return tr("{0} · 已选 {1}/{2}，点击全选", [platform.description, count.selected, count.runnable])
  }

  const actionLabels = platformActionLabels(platform, platformHasSelection(platform))
    .filter((label) => label !== tr("构建"))
  if (platform.targets.some(isTagPushTarget)) parts.push(tr("Tag 上传后由 GitHub 自动构建"))
  else if (platform.targets.some((target) => target.runner.type.trim().toLowerCase() === 'git-push')) parts.push(tr("推送后云端构建"))
  else if (actionLabels.length) parts.push(actionLabels.join('、'))
  if (platformPartiallyAvailable(platform)) parts.push(tr("部分步骤不可用"))
  return parts.join(' · ')
}

function togglePlatform(platform: ProductPlatform, checked: boolean) {
  if (checked) gitOnly.value = false
  for (const target of platformRunnableTargets(platform)) {
    const choice = targetChoices.value[target.id]
    if (!choice) continue
    choice.selected = checked
    if (checked) {
      for (const phase of phaseOptions.value) choice[phase.key] = !!target.steps[phase.key]
    }
  }
}

function toggleGitOnly(checked: boolean) {
  gitOnly.value = checked
  if (!checked) return
  for (const choice of Object.values(targetChoices.value)) choice.selected = false
}

const stageLabel = computed<Record<string, string>>(() => ({
  preparing: tr("准备发布"), versioning: tr("更新版本"), checking: tr("发布前检查"), committing: tr("创建提交"),
  building_targets: tr("检查、构建和打包"), publishing_targets: tr("上传和部署"),
  target_check: tr("检查目标"), target_build: tr("构建目标"), target_package: tr("打包目标"),
  target_publish: tr("上传目标"), target_deploy: tr("部署目标"), tagging: tr("创建 Tag"),
  pushing_branch: tr("推送分支"), pushing_tag: tr("推送 Tag"), completed: tr("发布完成"),
}))
const targetStageLabel = computed<Record<string, string>>(() => ({
  waiting: tr("等待执行"), checking: tr("检查"), check: tr("检查"), build: tr("构建"), package: tr("打包"),
  ready_to_publish: tr("等待上传或部署"), waiting_publish: tr("等待上传或部署"), publish: tr("上传"),
  deploy: tr("部署"), artifacts: tr("核对产物"), triggered: tr("已触发云端流程"), remote_pending: tr("等待云端处理"),
  cloud_pending: tr("已交给 GitHub"), completed: tr("已完成"),
}))

const summaryLines = computed(() => {
  const lines: string[] = []
  if (createTag.value) {
    for (const version of plannedVersions.value) lines.push(tr("{0}：{1}（{2}）", [version.versionGroupName, version.targetVersion || tr("待填写"), version.tagName || tr("待生成 Tag")]))
  }
  else lines.push(tr("不创建版本 Tag"))
  const fileCount = selectedPaths.value.length
  if (!fileCount && !createTag.value) lines.push(pushRemote.value ? tr("上传当前分支") : tr("不创建新提交"))
  else if (!fileCount) lines.push(pushRemote.value ? tr("创建并上传 Tag") : tr("创建本地 Tag"))
  if (pushRemote.value) {
    lines.push(tr("提交后上传到{0}", [remoteDestination.value]))
    if (preflight.value?.aheadCount) lines.push(tr("同时上传本机已有的 {0} 次提交", [preflight.value.aheadCount]))
  } else {
    lines.push(tr("只保存在本机，不上传远程仓库"))
  }
  if (gitOnly.value) {
    lines.push(tr("不构建平台"))
  } else {
    for (const platform of productPlatforms.value.filter(platformHasSelection)) {
      const partial = platformPartiallySelected(platform) ? tr("（部分目标）") : ''
      const cloudBuild = platform.targets.some((target) => isTagPushTarget(target) && targetChoices.value[target.id]?.selected)
      const actions = platformActionLabels(platform, true).filter((label) => !cloudBuild || label !== tr("上传"))
      if (cloudBuild) actions.unshift(tr("交给 GitHub 自动构建"))
      lines.push(tr("{0}{1}：{2}", [platform.name, partial, actions.join('、') || tr("尚未选择执行动作")]))
    }
    const advancedTargets = chosenTargets.value.filter(({ target }) => platformIdForTarget(target) === null)
    if (advancedTargets.length) lines.push(tr("高级目标：{0}", [advancedTargets.map(({ target }) => target.name).join('、')]))
  }
  if (willTriggerAutomation.value) {
    lines.push(willBuildTargetsInAutomation.value
      ? tr("Tag 上传后交给 GitHub 自动构建；完成后可在 GitHub 查看结果")
      : tr("Tag 上传后创建源码 Release，不生成安装包"))
  }
  return lines
})

function messageOf(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason)
}

function githubRepositoryUrl(remoteUrl: string) {
  const value = remoteUrl.trim().replace(/\.git$/i, '')
  const httpsMatch = value.match(/^https?:\/\/github\.com\/([^/]+)\/([^/]+)$/i)
  if (httpsMatch) return `https://github.com/${httpsMatch[1]}/${httpsMatch[2]}`
  const sshMatch = value.match(/^(?:ssh:\/\/)?git@github\.com[:/]([^/]+)\/([^/]+)$/i)
  if (sshMatch) return `https://github.com/${sshMatch[1]}/${sshMatch[2]}`
  return ''
}

function githubActionsUrl(remoteUrl: string, workflow: string) {
  const repository = githubRepositoryUrl(remoteUrl)
  if (!repository) return ''
  return workflow ? `${repository}/actions/workflows/${encodeURIComponent(workflow)}` : `${repository}/actions`
}

function nextPatchVersion(values: string[]) {
  let best = [0, 0, 0]
  let found = false
  for (const raw of values) {
    const match = raw.replace(/^v/, '').match(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/)
    if (!match) continue
    const current = match.slice(1).map(Number)
    const isHigher = current[0] > best[0]
      || (current[0] === best[0] && current[1] > best[1])
      || (current[0] === best[0] && current[1] === best[1] && current[2] > best[2])
    if (!found || isHigher) {
      best = current
      found = true
    }
  }
  if (!found) return '0.1.0'
  return `${best[0]}.${best[1]}.${best[2] + 1}`
}

function suggestReleaseVersion(currentVersions: string[], latestTag: string) {
  const canReleaseCurrentV2 = currentVersions.length > 0
    && currentVersions.every((version) => version.trim() === '2.0.0')
    && (() => {
      const latest = latestTag.replace(/^v/, '').match(/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/)
      if (!latest) return true
      return Number(latest[1]) < 2
    })()
  return canReleaseCurrentV2 ? '2.0.0' : nextPatchVersion([...currentVersions, latestTag])
}

function cloneConfig(config: ReleaseConfig): ReleaseConfig {
  return JSON.parse(JSON.stringify(config)) as ReleaseConfig
}

function normalizeConfig(raw: ReleaseConfig): ReleaseConfig {
  return {
    schemaVersion: 1,
    source: raw.source === 'file' ? 'file' : 'detected',
    repoRoot: raw.repoRoot || '',
    configPath: raw.configPath || '.launcher/release.yaml',
    confidence: Number.isFinite(raw.confidence) ? raw.confidence : 0,
    versionGroups: (raw.versionGroups || []).map((group) => ({
      id: group.id || 'product',
      name: group.name || group.id || tr("统一版本"),
      ...(group.tagPrefix ? { tagPrefix: group.tagPrefix } : {}),
      ...(group.currentVersion ? { currentVersion: group.currentVersion } : {}),
      versionFiles: (group.versionFiles || []).map((file) => ({
        path: file.path || '',
        format: file.format || 'json',
        ...(file.jsonPointer ? { jsonPointer: file.jsonPointer } : {}),
      })),
    })),
    targets: (raw.targets || []).map((target) => ({
      id: target.id || `target-${Date.now()}`,
      name: target.name || target.id || tr("未命名目标"),
      kind: target.kind || 'custom',
      versionGroup: target.versionGroup || raw.versionGroups?.[0]?.id || 'product',
      workingDir: target.workingDir || '.',
      runner: { type: target.runner?.type || 'local', os: target.runner?.os || [] },
      enabled: target.enabled !== false,
      detected: target.detected !== false,
      confidence: Number.isFinite(target.confidence) ? target.confidence : 0,
      steps: {
        check: target.steps?.check || '', build: target.steps?.build || '', package: target.steps?.package || '',
        publish: target.steps?.publish || '', deploy: target.steps?.deploy || '',
      },
      artifacts: target.artifacts || [],
    })),
    ...(raw.automation ? {
      automation: {
        provider: raw.automation.provider || '',
        workflow: raw.automation.workflow || '',
        trigger: raw.automation.trigger || 'tag',
        releaseBranch: raw.automation.releaseBranch || '',
        publishesRelease: raw.automation.publishesRelease === true,
      },
    } : {}),
    warnings: raw.warnings || [],
  }
}

function currentOS() {
  const platform = (navigator.platform || navigator.userAgent).toLowerCase()
  if (platform.includes('win')) return 'windows'
  if (platform.includes('mac')) return 'darwin'
  return 'linux'
}

function osLabel(os: string) {
  return ({ windows: 'Windows', linux: 'Linux', darwin: 'macOS' } as Record<string, string>)[os] || os
}

function targetUnavailableReason(target: ReleaseTarget) {
  if (!target.enabled) return tr("此目标已在配置中停用")
  const runnerType = target.runner.type.trim().toLowerCase()
  if (runnerType === 'git-push') return ''
  if (runnerType !== 'local') return tr("当前版本不支持此执行方式")
  if (!target.runner.os.length) return ''
  const supported = target.runner.os.map((value) => value.trim().toLowerCase())
  if (supported.includes('any') || supported.includes(currentOS())) return ''
  return tr("需要 {0} 环境，当前电脑不能执行", [target.runner.os.join(' / ')])
}

function targetPhaseHint(target: ReleaseTarget, phase: (typeof phaseOptions.value)[number]) {
  if (isTagPushTarget(target) && phase.key === 'publish') return tr("Tag 上传后由 GitHub 自动构建")
  if (target.runner.type.trim().toLowerCase() === 'git-push' && phase.key === 'publish') return tr("推送后触发云端构建")
  return target.steps[phase.key] ? phase.hint : tr("未配置")
}

function targetAvailable(target: ReleaseTarget) {
  return !targetUnavailableReason(target)
}

function defaultTargetChoice(target: ReleaseTarget): TargetChoice {
  return { selected: false, build: !!target.steps.build, package: !!target.steps.package, publish: !!target.steps.publish, deploy: !!target.steps.deploy }
}

function applyReleaseConfig(raw: ReleaseConfig, editing = false) {
  const normalized = normalizeConfig(raw)
  releaseConfig.value = normalized
  configDraft.value = cloneConfig(normalized)
  const next: Record<string, TargetChoice> = {}
  for (const target of normalized.targets) next[target.id] = targetChoices.value[target.id] || defaultTargetChoice(target)
  targetChoices.value = next
  if (!normalized.targets.length) gitOnly.value = true
  configEditorOpen.value = editing
}

function resetSelection(pf: ReleasePreflight) {
  const next: Record<string, boolean> = {}
  for (const file of pf.changes || []) next[file.path] = file.tracked
  selected.value = next
}

function fileStatusLabel(file: ReleaseFileChange) {
  if (isAddedFile(file)) return tr("新增")
  if (file.status.includes('D')) return tr("删除")
  if (file.status.includes('R')) return tr("重命名")
  return tr("修改")
}

function isAddedFile(file: ReleaseFileChange) {
  return !file.tracked || file.status.includes('A')
}

function selectAllFiles(checked: boolean) {
  for (const file of preflight.value?.changes || []) selected.value[file.path] = checked
}

function normalizePreflight(pf: ReleasePreflight): ReleasePreflight {
  return { ...pf, remoteChecked: pf.remoteChecked !== false, remotes: pf.remotes || [], versionFiles: pf.versionFiles || [], currentVersions: pf.currentVersions || {}, latestGroupTags: pf.latestGroupTags || {}, suggestedVersions: pf.suggestedVersions || {}, changes: pf.changes || [], aheadCount: pf.aheadCount || 0, unpushedChanges: pf.unpushedChanges || [], blockingIssues: pf.blockingIssues || [] }
}

function preferenceKey() {
  return `launcher.release-preferences.${props.app.id}`
}

function readLocalPreferences(): { createTag?: boolean; versionMode?: ReleaseVersionMode } {
  try { return JSON.parse(localStorage.getItem(preferenceKey()) || '{}') as { createTag?: boolean; versionMode?: ReleaseVersionMode } }
  catch { return {} }
}

function rememberPreferences() {
  if (!profileReady.value) return
  localStorage.setItem(preferenceKey(), JSON.stringify({ createTag: createTag.value, versionMode: versionMode.value }))
  if (preferenceTimer) clearTimeout(preferenceTimer)
  preferenceTimer = setTimeout(() => {
    const body = profileBody()
    preferenceSave = preferenceSave.catch(() => undefined).then(async () => {
      await api.saveReleaseProfile(props.app.id, body)
    })
  }, 250)
}

function setDefaultCommitMessage(force = false) {
  if (commitMessageDirty.value && !force) return
  commitMessage.value = createTag.value ? `chore(release): ${plannedTagNames.value.join(', ')}` : `chore: update ${props.app.name}`
  commitMessageDirty.value = false
}

function onCommitMessageInput() {
  commitMessageDirty.value = true
}

function syncVersionInputs() {
  const next = { ...versionInputs.value }
  for (const version of plannedVersions.value) {
    if (!next[version.versionGroupId] || versionMode.value === 'auto') next[version.versionGroupId] = version.suggestedVersion
  }
  versionInputs.value = next
}

function scheduleReleaseNotesDraft(delay = 80) {
  if (!createTag.value || !preflight.value || releaseNotes.value || releaseNotesDirty.value || disposed) return
  if (releaseNotesTimer) clearTimeout(releaseNotesTimer)
  releaseNotesTimer = setTimeout(() => void generateReleaseNotesDraft(), delay)
}

async function generateReleaseNotesDraft(force = false, overwriteConfirmed = false) {
  const pf = preflight.value
  if (!pf || !createTag.value || releaseNotesLoading.value) return
  // A scheduled draft may start after the user has begun typing.
  if (!force && (releaseNotes.value || releaseNotesDirty.value)) return
  if (force && releaseNotesDirty.value && !overwriteConfirmed) {
    confirmAction.value = 'regenerate-notes'
    return
  }

  const sourceSignature = releaseNotesOptionsSignature.value
  const requestId = ++releaseNotesRequest
  releaseNotesLoading.value = true
  releaseNotesError.value = ''
  try {
    const draft = await api.createReleaseNotesDraft(props.app.id, {
      statusFingerprint: pf.statusFingerprint,
      selectedPaths: selectedPaths.value,
      selectedTargets: selectedTargets.value,
    })
    if (disposed || requestId !== releaseNotesRequest) return
    if (sourceSignature !== releaseNotesOptionsSignature.value) {
      releaseNotesStale.value = true
      scheduleReleaseNotesDraft(120)
      return
    }
    releaseNotes.value = draft.text
    releaseNotesBaseTag.value = draft.baseTag
    releaseNotesSourceFingerprint.value = draft.sourceFingerprint
    releaseNotesGeneratedFor.value = sourceSignature
    releaseNotesDirty.value = false
    releaseNotesStale.value = false
  } catch (reason) {
    if (requestId === releaseNotesRequest) releaseNotesError.value = messageOf(reason)
  } finally {
    if (requestId === releaseNotesRequest) releaseNotesLoading.value = false
  }
}

function onReleaseNotesInput() {
  if (releaseNotesTimer) {
    clearTimeout(releaseNotesTimer)
    releaseNotesTimer = null
  }
  releaseNotesRequest += 1
  releaseNotesLoading.value = false
  if (!releaseNotesGeneratedFor.value) releaseNotesGeneratedFor.value = releaseNotesOptionsSignature.value
  releaseNotesDirty.value = true
  releaseNotesError.value = ''
}

function applyPreflight(raw: ReleasePreflight, initial = false, resetFiles = true) {
  const pf = normalizePreflight(raw)
  preflight.value = pf
  remoteName.value = pf.profile?.remoteName || remoteName.value || 'origin'
  versionStrategy.value = pf.profile?.versionStrategy || versionStrategy.value || 'auto'
  preReleaseCommand.value = pf.profile?.preReleaseCommand || ''
  if (initial) {
    const remembered = readLocalPreferences()
    createTag.value = remembered.createTag ?? (typeof pf.profile?.createTag === 'boolean' ? pf.profile.createTag : true)
    versionMode.value = remembered.versionMode || (pf.profile?.versionMode === 'manual' || pf.profile?.versionMode === 'auto' ? pf.profile.versionMode : 'auto')
  }
  if (createTag.value) syncVersionInputs()
  setDefaultCommitMessage(initial)
  if (resetFiles) resetSelection(pf)
  preflightStale.value = false
  scheduleReleaseNotesDraft()
}

async function load(resumeFailedRun = true) {
  loading.value = true
  checkingRemote.value = false
  error.value = ''
  configNotice.value = ''
  try {
    const localPreflight = api.releasePreflight(props.app.id, false)
    const [historyResult, configResult] = await Promise.allSettled([
      api.listReleases(props.app.id), api.getReleaseConfig(props.app.id),
    ] as const)
    if (disposed) return
    history.value = historyResult.status === 'fulfilled' ? historyResult.value : []
    if (configResult.status === 'fulfilled') {
      configEndpointAvailable.value = true
      applyReleaseConfig(configResult.value)
    } else {
      configEndpointAvailable.value = false
      gitOnly.value = true
      configNotice.value = tr("当前后端暂未启用自动发布配置，仍可继续使用基础 Git 提交与 Tag 功能。")
    }
    loading.value = false

    const saved = readReleaseSession()
    const savedRun = saved?.appId === props.app.id ? history.value.find((run) =>
      run.id === saved.runId || (!saved.runId && saved.submittedAt &&
        Date.parse(run.createdAt.includes('T') ? run.createdAt : run.createdAt.replace(' ', 'T') + 'Z') >= saved.submittedAt - 1000)
    ) : undefined
    const resumable = savedRun || history.value.find((run) => run.status === 'queued' || run.status === 'running')
      || (resumeFailedRun ? history.value.find(canResumeFailedRun) : undefined)
    if (resumable) showRun(resumable)

    applyPreflight(await localPreflight, true)
    profileReady.value = true
    if (!resumable && !disposed) {
      checkingRemote.value = true
      try {
        const remotePreflight = await api.releasePreflight(props.app.id)
        if (!disposed) applyPreflight(remotePreflight, false, false)
      } catch (reason) {
        if (!disposed) {
          error.value = messageOf(reason)
          preflightStale.value = true
        }
      } finally {
        if (!disposed) checkingRemote.value = false
      }
    }
  } catch (reason) {
    error.value = messageOf(reason)
  } finally {
    profileReady.value = true
    loading.value = false
  }
}

function profileBody() {
  return { remoteName: remoteName.value, versionStrategy: versionStrategy.value, preReleaseCommand: preReleaseCommand.value, createTag: createTag.value, versionMode: versionMode.value }
}

async function saveAndRecheck() {
  savingProfile.value = true
  checkingRemote.value = true
  error.value = ''
  preflightStale.value = true
  try {
    await api.saveReleaseProfile(props.app.id, profileBody())
    applyPreflight(await api.releasePreflight(props.app.id))
  } catch (reason) { error.value = messageOf(reason) }
  finally { savingProfile.value = false; checkingRemote.value = false }
}

function onVersionInput(groupID: string, value: string) {
  versionInputs.value = { ...versionInputs.value, [groupID]: value.trim() }
  setDefaultCommitMessage()
}
function onCreateTagChange() {
  if (createTag.value) {
    syncVersionInputs()
    scheduleReleaseNotesDraft()
  } else {
    releaseNotesError.value = ''
    releaseNotesLoading.value = false
    releaseNotesRequest += 1
  }
  setDefaultCommitMessage()
}
function onVersionModeChange() {
  syncVersionInputs()
  setDefaultCommitMessage()
}
function setTargetSelected(targetId: string, checked: boolean) {
  const choice = targetChoices.value[targetId]
  const target = configuredTargets.value.find((item) => item.id === targetId)
  if (!choice || !target) return
  choice.selected = checked
  if (checked) {
    gitOnly.value = false
    for (const phase of phaseOptions.value) choice[phase.key] = !!target.steps[phase.key]
  }
}
function setTargetPhase(targetId: string, phase: ExecutionPhase, checked: boolean) {
  if (targetChoices.value[targetId]) targetChoices.value[targetId][phase] = checked
  if (checked) gitOnly.value = false
}
function versionGroupName(target: ReleaseTarget) {
  const group = releaseConfig.value?.versionGroups.find((item) => item.id === target.versionGroup)
  return group ? versionGroupDisplayName(group) : target.versionGroup || tr("统一版本")
}

async function scanReleaseConfig() {
  configScanning.value = true
  configValidationError.value = ''
  configNotice.value = ''
  try {
    const previous = releaseConfig.value ? cloneConfig(releaseConfig.value) : null
    applyReleaseConfig(await api.scanReleaseConfig(props.app.id), true)
    advancedOpen.value = true
    configBeforeEdit.value = previous
    configEndpointAvailable.value = true
    configNotice.value = tr("自动识别已完成。请检查建议；点击“保存并使用”后才会写入项目。")
  } catch (reason) {
    if (!releaseConfig.value) configEndpointAvailable.value = false
    configNotice.value = tr("自动识别暂不可用：{0}。基础 Git 发布仍可使用。", [messageOf(reason)])
  } finally { configScanning.value = false }
}

function openConfigEditor() {
  if (!releaseConfig.value) { void scanReleaseConfig(); return }
  advancedOpen.value = true
  configBeforeEdit.value = cloneConfig(releaseConfig.value)
  configDraft.value = cloneConfig(releaseConfig.value)
  configEditorOpen.value = true
  configValidationError.value = ''
}
function cancelConfigEdit() {
  if (configBeforeEdit.value) applyReleaseConfig(configBeforeEdit.value)
  else configEditorOpen.value = false
  configBeforeEdit.value = null
  configDraft.value = releaseConfig.value ? cloneConfig(releaseConfig.value) : null
  configValidationError.value = ''
}
function validateConfig(config: ReleaseConfig) {
  if (!config.versionGroups.length) return tr("至少需要一个版本组。")
  const groupIds = config.versionGroups.map((group) => group.id.trim())
  if (groupIds.some((id) => !id)) return tr("版本组标识不能为空。")
  if (new Set(groupIds).size !== groupIds.length) return tr("版本组标识不能重复。")
  const tagPrefixes = config.versionGroups.map((group) => (group.tagPrefix || group.id).trim().toLowerCase())
  if (tagPrefixes.some((prefix) => !/^[a-z0-9][a-z0-9._-]*$/i.test(prefix))) return tr("Tag 前缀只能包含字母、数字、点、下划线和短横线。")
  if (new Set(tagPrefixes).size !== tagPrefixes.length) return tr("每个版本组的 Tag 前缀必须不同。")
  const targetIds = config.targets.map((target) => target.id.trim())
  if (targetIds.some((id) => !id)) return tr("发布目标标识不能为空。")
  if (new Set(targetIds).size !== targetIds.length) return tr("发布目标标识不能重复。")
  const invalidTarget = config.targets.find((target) => !target.name.trim() || !groupIds.includes(target.versionGroup))
  if (invalidTarget) return tr("目标“{0}”缺少名称或有效版本组。", [invalidTarget.name || invalidTarget.id])
  return ''
}
async function saveReleaseConfig() {
  if (!configDraft.value) return
  const validationError = validateConfig(configDraft.value)
  if (validationError) { configValidationError.value = validationError; return }
  configSaving.value = true
  configValidationError.value = ''
  configNotice.value = ''
  let savedSuccessfully = false
  try {
    const saved = await api.saveReleaseConfig(props.app.id, normalizeConfig(configDraft.value))
    savedSuccessfully = true
    applyReleaseConfig(saved)
    configBeforeEdit.value = null
    configNotice.value = tr("发布说明书已保存到 {0}。", [saved.configPath || '.launcher/release.yaml'])
    preflightStale.value = true
    applyPreflight(await api.releasePreflight(props.app.id))
  } catch (reason) {
    const message = messageOf(reason)
    configValidationError.value = message
    if (savedSuccessfully) error.value = tr("配置已保存，但重新检查 Git 失败：{0}", [message])
  }
  finally { configSaving.value = false }
}

function onAdvancedToggle(event: Event) {
  advancedOpen.value = (event.target as HTMLDetailsElement).open
}

function newId(prefix: string, existing: string[]) {
  let index = existing.length + 1
  while (existing.includes(`${prefix}-${index}`)) index += 1
  return `${prefix}-${index}`
}
function addVersionGroup() {
  if (!configDraft.value) return
  const id = newId('version', configDraft.value.versionGroups.map((group) => group.id))
  configDraft.value.versionGroups.push({ id, name: tr("新版本组"), tagPrefix: id, versionFiles: [] })
}
function removeVersionGroup(index: number) {
  const config = configDraft.value
  if (!config || config.versionGroups.length <= 1) return
  const [removed] = config.versionGroups.splice(index, 1)
  const fallback = config.versionGroups[0].id
  for (const target of config.targets) if (target.versionGroup === removed.id) target.versionGroup = fallback
}
function addVersionFile(group: ReleaseVersionGroup) { group.versionFiles.push({ path: '', format: 'json', jsonPointer: '/version' }) }
function addTarget() {
  const config = configDraft.value
  if (!config) return
  const id = newId('target', config.targets.map((target) => target.id))
  const emptySteps: ReleaseTargetSteps = { check: '', build: '', package: '', publish: '', deploy: '' }
  config.targets.push({ id, name: tr("新发布目标"), kind: 'custom', versionGroup: config.versionGroups[0]?.id || 'product', workingDir: '.', runner: { type: 'local', os: [currentOS()] }, enabled: true, detected: false, confidence: 1, steps: emptySteps, artifacts: [] })
}
function setRunnerOS(target: ReleaseTarget, os: string, checked: boolean) {
  const values = new Set(target.runner.os)
  if (checked) values.add(os); else values.delete(os)
  target.runner.os = [...values]
}
function setArtifacts(target: ReleaseTarget, value: string) { target.artifacts = value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean) }

async function publish() {
  const pf = preflight.value
  if (!pf || !canPublish.value) return
  publishing.value = true
  error.value = ''
  try {
    await api.saveReleaseProfile(props.app.id, profileBody())
    rememberReleaseSession({ appId: props.app.id, submittedAt: Date.now() })
    const run = await api.createRelease(props.app.id, {
      targetVersion: createTag.value ? primaryTargetVersion.value : '',
      versions: createTag.value ? plannedVersions.value.map((version) => ({ versionGroupId: version.versionGroupId, targetVersion: version.targetVersion })) : [],
      createTag: createTag.value, versionMode: versionMode.value,
      pushRemote: pushRemote.value,
      selectedTargets: selectedTargets.value, selectedPaths: selectedPaths.value, commitMessage: commitMessage.value, statusFingerprint: pf.statusFingerprint,
      releaseNotes: createTag.value ? releaseNotes.value.trim() : '',
      releaseNotesConfirmed: createTag.value,
      externalActionsConfirmed: hasExternalAction.value,
    })
    if (!disposed) showRun(run)
  } catch (reason) { error.value = messageOf(reason) }
  finally { publishing.value = false }
}

function showRun(run: ReleaseRun) {
  if (pollTimer) clearTimeout(pollTimer)
  activeRun.value = run
  logs.value = []
  runTargets.value = []
  runArtifacts.value = []
  runAutomation.value = null
  error.value = ''
  rememberReleaseSession({ appId: props.app.id, runId: run.id })
  schedulePoll(0)
}

function historyStatus(run: ReleaseRun) {
  if (run.status === 'succeeded') return run.pushRemote ? tr("已推送") : tr("本地完成")
  return run.status === 'failed' ? tr("失败") : tr("进行中")
}

function schedulePoll(delay = 700) {
  if (disposed) return
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = setTimeout(() => void poll(), delay)
}
async function poll() {
  const run = activeRun.value
  if (!run || disposed) return
  try {
    const lastId = logs.value.length ? logs.value[logs.value.length - 1].id : 0
    const view = await api.getReleaseRun(run.id, lastId)
    if (disposed || activeRun.value?.id !== run.id) return
    activeRun.value = view.run
    runTargets.value = view.targets || []
    runArtifacts.value = view.artifacts || []
    runAutomation.value = view.automation || null
    logs.value = [...logs.value, ...(view.logs || [])]
    if (view.run.status === 'queued' || view.run.status === 'running') schedulePoll()
    else history.value = await api.listReleases(props.app.id)
  } catch (reason) {
    if (disposed) return
    error.value = messageOf(reason)
    schedulePoll(1500)
  }
}
async function retry(externalActionsConfirmed = false) {
  if (!activeRun.value || !retryable.value) return
  if (externalRetryRisk.value && !externalActionsConfirmed) {
    confirmAction.value = 'retry'
    return
  }
  error.value = ''
  try { activeRun.value = await api.retryRelease(activeRun.value.id, externalRetryRisk.value && externalActionsConfirmed); schedulePoll(0) }
  catch (reason) { error.value = messageOf(reason) }
}
async function confirmSensitiveAction() {
  const action = confirmAction.value
  confirmAction.value = null
  if (action === 'retry') await retry(true)
  else if (action === 'regenerate-notes') await generateReleaseNotesDraft(true, true)
}
function startNew() {
  if (pollTimer) clearTimeout(pollTimer)
  rememberReleaseSession({ appId: props.app.id })
  confirmAction.value = null
  activeRun.value = null
  logs.value = []
  runTargets.value = []
  runArtifacts.value = []
  runAutomation.value = null
  releaseNotes.value = ''
  releaseNotesDirty.value = false
  releaseNotesStale.value = false
  releaseNotesGeneratedFor.value = ''
  releaseNotesError.value = ''
  commitMessageDirty.value = false
  void load(false)
}

function closePushMenuOnOutsideClick(event: PointerEvent) {
  if (pushMenuOpen.value && event.target instanceof Node && !publishControlRef.value?.contains(event.target)) pushMenuOpen.value = false
}

watch([createTag, versionMode], rememberPreferences)
watch([() => activeRun.value?.id, () => activeRun.value?.status], async () => {
  await nextTick()
  bodyRef.value?.scrollTo({ top: 0 })
})
watch(releaseNotesOptionsSignature, (signature) => {
  if (!createTag.value) return
  if (releaseNotesGeneratedFor.value && signature === releaseNotesGeneratedFor.value) releaseNotesStale.value = false
  else if (releaseNotesGeneratedFor.value) releaseNotesStale.value = true
  else if (!releaseNotes.value && !releaseNotesDirty.value) scheduleReleaseNotesDraft()
})
watch(plannedTagNames, (tags) => {
  if (profileReady.value && createTag.value && versionMode.value === 'auto' && tags.length) {
    syncVersionInputs()
    setDefaultCommitMessage()
  }
})
onMounted(() => {
  disposed = false
  document.addEventListener('pointerdown', closePushMenuOnOutsideClick)
  void load()
})
onBeforeUnmount(() => {
  disposed = true
  document.removeEventListener('pointerdown', closePushMenuOnOutsideClick)
  if (pollTimer) clearTimeout(pollTimer)
  if (preferenceTimer) clearTimeout(preferenceTimer)
  if (releaseNotesTimer) clearTimeout(releaseNotesTimer)
  releaseNotesRequest += 1
})
</script>

<template>
  <div class="overlay" @click.self="publishing || emit('close')">
    <div class="modal">
      <header class="m-head">
        <h2>{{ tr("发布") }} {{ app.name }}</h2>
        <button class="ghost icon" :disabled="publishing" @click="emit('close')">✕</button>
      </header>

      <div ref="bodyRef" class="m-body" :inert="publishing">
        <div v-if="loading" class="state">{{ tr("正在读取发布配置…") }}</div>
        <div v-if="error" class="alert error">{{ error }}</div>
        <div v-if="preflightStale" class="alert warn">{{ tr("配置或 Git 已变化，请重新检查。") }}<button :disabled="savingProfile" @click="saveAndRecheck">{{ tr("重新检查") }}</button></div>
        <div v-if="isActive" class="alert warn">{{ tr("项目正在运行；发布不会自动停止或重启。") }}</div>

        <template v-if="activeRun">
          <section class="progress-block">
            <section v-if="activeRun.status === 'succeeded'" class="completion-banner" role="status" aria-live="polite">
              <span class="completion-icon" aria-hidden="true">✓</span>
              <h3>{{ completionTitle }}</h3>
              <p>{{ completionDescription }}</p>
              <template v-if="automationHandedOff">
                <div class="completion-next"><strong>{{ cloudExecutionNotice?.title || tr("后续由 GitHub Actions 执行") }}</strong><span>{{ cloudExecutionNotice?.text }}</span><span class="cloud-result-pending">{{ tr("云端结果尚未确认，请到 GitHub 查看最终发布结果。") }}</span></div>
                <a v-if="automationPageUrl" class="actions-link" :href="automationPageUrl" target="_blank" rel="noreferrer">{{ tr("查看 GitHub Actions 进度") }} <span aria-hidden="true">↗</span></a>
              </template>
            </section>
            <div v-else-if="cloudExecutionNotice" class="cloud-execution-notice" role="note"><strong>{{ cloudExecutionNotice.title }}</strong><p>{{ cloudExecutionNotice.text }}</p></div>
            <div class="progress-title"><strong>{{ activeRun.createTag === false ? tr("代码更新") : (activeRun.versions?.map(version => version.tagName).join('、') || activeRun.tagName) }}</strong><span v-if="activeRun.status !== 'succeeded'" class="status" :class="activeRun.status">{{ activeRunStatusLabel }}</span></div>
            <div v-if="activeRun.status !== 'succeeded'" class="current-stage">{{ activeStageLabel }}</div>
            <div v-if="runTargets.length" class="run-targets"><div v-for="target in runTargets" :key="target.targetId" class="run-target"><strong>{{ configuredTargets.find((item) => item.id === target.targetId)?.name || target.targetId }}</strong><span>{{ targetStageLabel[target.stage] || stageLabel[target.stage] || tr("等待执行") }}</span><em :class="target.status">{{ target.status === 'succeeded' ? tr("完成") : target.status === 'failed' ? tr("失败") : target.status === 'running' ? tr("执行中") : ['triggered', 'remote_pending', 'handed_off'].includes(target.status) ? tr("已交接") : tr("等待") }}</em></div></div>
            <details class="execution-details" :open="activeRun.status !== 'succeeded'"><summary>{{ tr("执行日志") }}</summary><div class="log-box"><div v-for="line in logs" :key="line.id" :class="['log-line', line.stream]">{{ line.text }}</div><div v-if="!logs.length" class="muted">{{ tr("等待发布日志…") }}</div></div></details>
            <details v-if="runArtifacts.length" class="artifacts"><summary>{{ tr('已生成产物（{0}）', [runArtifacts.length]) }}</summary><div v-for="artifact in runArtifacts" :key="`${artifact.targetId}-${artifact.path}`" class="artifact-row"><code>{{ artifact.path }}</code><span>{{ Math.max(1, Math.round(artifact.sizeBytes / 1024)) }} KB</span><code>{{ artifact.sha256.slice(0, 12) }}</code></div></details>
            <div v-if="activeRun.errorMessage" class="alert error">{{ tr(activeRun.errorMessage) }}</div>
            <div v-if="activeRun.status === 'failed' && externalRetryRisk" class="alert warn">{{ tr("上传或部署的远端结果可能已经生效。重试前请先检查目标平台，避免重复上传或重复上线。") }}</div>
            <div v-if="activeRun.commitSha" class="kv"><span>{{ tr("提交") }}</span><code>{{ activeRun.commitSha }}</code></div>
            <div class="button-row"><button v-if="retryable" class="primary" @click="retry()">{{ externalRetryRisk ? tr("确认远端未成功后重试") : tr("从失败阶段重试") }}</button><button v-if="activeRun.status === 'succeeded' || activeRun.status === 'failed'" @click="startNew">{{ tr("返回发布检查") }}</button></div>
          </section>
        </template>

        <template v-else-if="(preflight || releaseConfig) && !loading">
          <section v-if="preflight" class="repo-glance" :class="{ problem: preflight.remoteChecked && !preflight.canRelease, checking: !preflight.remoteChecked }">
            <span class="ready-dot"></span>
            <strong>{{ preflight.branch || tr("未绑定分支") }}</strong>
            <span>{{ preflight.latestTag ? tr("最新 Tag：{0}", [preflight.latestTag]) : tr("暂无 Tag") }}</span>
            <span class="repo-glance-status" :title="!preflight.remoteChecked ? tr('面板可以先操作；发布按钮会在远程分支检查完成后启用') : preflight.canRelease ? tr('分支已同步；未发现冲突、进行中的 Git 操作或暂存文件') : tr('请按下方提示处理仓库问题')">{{ !preflight.remoteChecked ? tr("正在检查远程分支…") : preflight.canRelease ? tr("仓库状态正常") : tr("仓库需要处理") }}</span>
          </section>
          <section v-else class="repo-glance checking"><span class="ready-dot"></span><strong>{{ tr("正在读取本地仓库…") }}</strong><span class="repo-glance-status">{{ tr("构建端可以先选择") }}</span></section>
          <section v-if="preflight?.blockingIssues.length" class="issues"><div v-for="issue in preflight.blockingIssues" :key="issue.code" class="alert error">{{ tr(issue.message) }}</div></section>

          <section class="platform-section">
            <div class="section-head basic-section-head"><h3>{{ tr("选择构建端") }}</h3></div>
            <div class="platform-grid">
              <button
                v-for="platform in productPlatforms"
                :key="platform.id"
                type="button"
                class="platform-card"
                :class="{ selected: platformSelected(platform), partial: platformPartiallySelected(platform), limited: platformPartiallyAvailable(platform), unavailable: !!platformUnavailableReason(platform) }"
                :disabled="!!platformUnavailableReason(platform)"
                @click="togglePlatform(platform, !platformSelected(platform))"
              >
                <span class="platform-icon">{{ platform.icon }}</span>
                <span class="platform-copy">
                  <strong>{{ platform.name }}</strong>
                  <small>{{ platformCardDetail(platform) }}</small>
                </span>
                <span v-if="platformSelected(platform) || platformPartiallySelected(platform)" class="chosen-mark">✓</span>
                <span v-if="platformActionLabels(platform, platformHasSelection(platform)).some((label) => label === tr('上传') || label === tr('部署上线'))" class="risk-badge">{{ tr("含上线操作") }}</span>
              </button>
              <button type="button" class="platform-card git-card" :class="{ selected: gitOnly }" @click="toggleGitOnly(!gitOnly)">
                <span class="platform-icon">⑂</span>
                <span class="platform-copy"><strong>{{ tr("仅提交代码") }}</strong><small>{{ createTag ? tr("同时创建 Tag") : tr("不创建 Tag") }}</small></span>
                <span v-if="gitOnly" class="chosen-mark">✓</span>
              </button>
            </div>
          </section>

          <template v-if="preflight">
          <section class="block version-quick">
            <div class="tag-switch-row"><div><h3>{{ tr("创建版本 Tag") }}</h3><p>{{ tr("每个版本组独立递增；同批 Tag 指向同一个提交。") }}</p></div><label class="switch"><input v-model="createTag" type="checkbox" @change="onCreateTagChange" /><span></span></label></div>
            <template v-if="createTag">
              <div class="mode-picker"><label :class="{ active: versionMode === 'auto' }"><input v-model="versionMode" type="radio" value="auto" @change="onVersionModeChange" />{{ tr("自动递增") }}</label><label :class="{ active: versionMode === 'manual' }"><input v-model="versionMode" type="radio" value="manual" @change="onVersionModeChange" />{{ tr("手动设置") }}</label></div>
              <div class="version-list">
                <div v-for="version in plannedVersions" :key="version.versionGroupId" class="version-row">
                  <span class="version-name"><strong>{{ version.versionGroupName }}</strong><small>{{ tr("当前") }} {{ version.currentVersion }}</small></span>
                  <input v-if="versionMode === 'manual'" :value="version.targetVersion" :class="{ invalid: version.targetVersion && !versionPattern.test(version.targetVersion) }" :placeholder="tr('例如 1.4.0')" @input="onVersionInput(version.versionGroupId, ($event.target as HTMLInputElement).value)" />
                  <span v-else class="version-next">→ <strong>{{ version.targetVersion }}</strong></span>
                  <code>{{ version.tagName }}</code>
                </div>
              </div>
              <div v-if="!versionValid" class="field-error">{{ tr("版本必须是 X.Y.Z，例如 1.4.0。") }}</div>
            </template>
            <div v-else class="no-tag-note">{{ tr("不更新版本号，也不创建 Tag。") }}</div>
          </section>

          <details class="advanced-settings" :open="advancedOpen" @toggle="onAdvancedToggle">
            <summary><span>{{ tr("高级设置") }}</span><small>{{ tr("Git、命令和发布配置") }}</small></summary>
            <div class="advanced-body">
          <section class="repo-card">
            <div class="kv"><span>{{ tr("代码仓库") }}</span><code>{{ preflight.repoRoot }}</code></div><div class="kv"><span>{{ tr("当前分支") }}</span><code>{{ preflight.branch || tr("未绑定分支") }}</code></div>
            <div class="kv"><span>{{ tr("远程地址") }}</span><code>{{ preflight.remoteUrl || '—' }}</code></div><div class="kv"><span>{{ tr("最新版本") }}</span><code>{{ preflight.latestTag || tr("还没有版本 Tag") }}</code></div>
          </section>

          <section class="block config-section">
            <div class="section-head">
              <div><h3>{{ tr("发布目标") }}</h3><div class="section-help">{{ tr("自动识别项目；日常发布只需勾选本次要处理的平台。") }}</div></div>
              <div v-if="configEndpointAvailable" class="toolbar"><button @click="scanReleaseConfig" :disabled="configScanning || configSaving || configEditorOpen">{{ configScanning ? tr("识别中…") : tr("重新自动识别") }}</button><button @click="openConfigEditor" :disabled="configSaving || configEditorOpen">{{ configEditorOpen ? tr("正在配置") : tr("修改配置") }}</button></div>
            </div>
            <div v-if="configNotice" class="alert info">{{ configNotice }}</div>
            <div v-if="releaseConfig" class="config-meta"><span>{{ releaseConfig.source === 'file' ? tr("已保存配置") : tr("自动识别建议") }}</span><span>{{ tr("识别可信度") }} {{ configConfidence }}%</span><code>{{ releaseConfig.configPath || '.launcher/release.yaml' }}</code></div>
            <div v-for="warning in releaseConfig?.warnings || []" :key="warning" class="alert warn">{{ warning }}</div>

            <template v-if="configEditorOpen && configDraft">
              <div class="wizard-banner"><strong>{{ tr("发布配置向导") }}</strong><span>{{ tr("系统已尽量自动填写。只有不正确的地方才需要修改，高级命令可展开查看。") }}</span></div>
              <div class="editor-subhead"><strong>{{ tr("版本组") }}</strong><button @click="addVersionGroup">{{ tr("＋ 添加版本组") }}</button></div>
              <div v-for="(group, groupIndex) in configDraft.versionGroups" :key="`${group.id}-${groupIndex}`" class="edit-card">
                <div class="form-grid three"><label>{{ tr("显示名称") }}<input v-model="group.name" :placeholder="tr('例如 客户端版本')" /></label><label>{{ tr("Tag 前缀") }}<input v-model="group.tagPrefix" :placeholder="tr('例如 desktop')" /></label><div class="field-action"><button class="danger" :disabled="configDraft.versionGroups.length <= 1" @click="removeVersionGroup(groupIndex)">{{ tr("删除版本组") }}</button></div></div>
                <details class="advanced"><summary>{{ tr('高级：需要同步修改的版本文件（{0}）', [group.versionFiles.length]) }}</summary>
                  <div v-for="(file, fileIndex) in group.versionFiles" :key="fileIndex" class="version-file-row"><input v-model="file.path" :placeholder="tr('文件路径，例如 package.json')" /><input v-model="file.format" list="version-formats" :placeholder="tr('格式')" /><input v-model="file.jsonPointer" :placeholder="tr('字段，例如 /version')" /><button class="ghost danger-text" @click="group.versionFiles.splice(fileIndex, 1)">{{ tr("删除") }}</button></div>
                  <button @click="addVersionFile(group)">{{ tr("＋ 添加版本文件") }}</button>
                </details>
              </div>
              <div class="editor-subhead"><strong>{{ tr("平台与交付目标") }}</strong><button @click="addTarget">{{ tr("＋ 添加目标") }}</button></div>
              <div v-if="!configDraft.targets.length" class="empty-config">{{ tr("没有识别到可发布目标。可手动添加，或者仍只使用 Git 提交与 Tag。") }}</div>
              <div v-for="(target, targetIndex) in configDraft.targets" :key="`${target.id}-${targetIndex}`" class="edit-card target-edit-card">
                <div class="target-edit-title"><label class="plain-check"><input v-model="target.enabled" type="checkbox" />{{ tr("启用这个目标") }}</label><button class="ghost danger-text" @click="configDraft.targets.splice(targetIndex, 1)">{{ tr("删除") }}</button></div>
                <div class="form-grid"><label>{{ tr("目标名称") }}<input v-model="target.name" :placeholder="tr('例如 Web 正式站')" /></label><label>{{ tr("目标标识") }}<input v-model="target.id" :placeholder="tr('例如 web-production')" /></label><label>{{ tr("类型") }}<input v-model="target.kind" list="target-kinds" placeholder="web / windows / android" /></label><label>{{ tr("使用版本组") }}<select v-model="target.versionGroup"><option v-for="group in configDraft.versionGroups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label><label>{{ tr("项目子目录") }}<input v-model="target.workingDir" placeholder="." /></label><label>{{ tr("执行方式") }}<input v-model="target.runner.type" list="runner-types" placeholder="local" /></label></div>
                <div class="os-row"><span>{{ tr("可执行系统") }}</span><label v-for="os in ['windows', 'linux', 'darwin']" :key="os" class="plain-check"><input type="checkbox" :checked="target.runner.os.includes(os)" @change="setRunnerOS(target, os, ($event.target as HTMLInputElement).checked)" />{{ osLabel(os) }}</label></div>
                <details class="advanced"><summary>{{ tr("高级：检查、构建、打包和上线命令") }}</summary><label class="full-label">{{ tr("发布前检查") }}<input v-model="target.steps.check" :placeholder="tr('例如 npm test')" /></label><div class="form-grid"><label>{{ tr("构建命令") }}<input v-model="target.steps.build" :placeholder="tr('例如 npm run build')" /></label><label>{{ tr("打包命令") }}<input v-model="target.steps.package" :placeholder="tr('例如 npm run tauri build')" /></label><label>{{ tr("上传命令") }}<input v-model="target.steps.publish" :placeholder="tr('可留空，发布时不会上传')" /></label><label>{{ tr("部署命令") }}<input v-model="target.steps.deploy" :placeholder="tr('可留空，发布时不会上线')" /></label></div><label class="full-label">{{ tr("产物位置（每行一个）") }}<textarea :value="target.artifacts.join('\n')" rows="3" :placeholder="tr('例如 dist/**/*')" @input="setArtifacts(target, ($event.target as HTMLTextAreaElement).value)"></textarea></label></details>
              </div>
              <div v-if="configValidationError" class="alert error">{{ configValidationError }}</div>
              <div class="editor-actions"><button @click="cancelConfigEdit">{{ tr("取消修改") }}</button><button class="primary" :disabled="configSaving" @click="saveReleaseConfig">{{ configSaving ? tr("保存并重新检查中…") : tr("保存并使用") }}</button></div>
            </template>

            <template v-else-if="releaseConfig?.targets.length">
              <div v-if="configNeedsSaving" class="setup-callout"><span>{{ tr("当前使用自动识别结果；需要调整时再保存配置。") }}</span><button class="primary" @click="openConfigEditor">{{ tr("修改并保存") }}</button></div>
              <label class="git-only-choice"><input type="checkbox" :checked="gitOnly" @change="toggleGitOnly(($event.target as HTMLInputElement).checked)" />{{ tr("本次仅提交代码 / 创建 Tag，不执行任何平台构建") }}</label>
              <div class="target-list">
                <article v-for="target in releaseConfig.targets" :key="target.id" class="target-card" :class="{ disabled: !targetAvailable(target) || gitOnly, selected: !gitOnly && targetAvailable(target) && targetChoices[target.id]?.selected, invalid: invalidChosenTargetIds.includes(target.id) }">
                  <header class="target-head"><label class="target-select"><input type="checkbox" :checked="!gitOnly && targetAvailable(target) && targetChoices[target.id]?.selected" :disabled="gitOnly || !targetAvailable(target)" @change="setTargetSelected(target.id, ($event.target as HTMLInputElement).checked)" /><span><strong>{{ target.name }}</strong><small>{{ target.kind }} · {{ versionGroupName(target) }}</small></span></label><span v-if="target.detected" class="detected-badge">{{ tr("自动识别") }}</span></header>
                  <div v-if="targetUnavailableReason(target)" class="unavailable">{{ targetUnavailableReason(target) }}</div>
                  <div v-else class="phase-grid"><label v-for="phase in phaseOptions" :key="phase.key" class="phase-choice" :class="{ unavailable: !target.steps[phase.key], risky: phase.risky && targetChoices[target.id]?.[phase.key] }"><input type="checkbox" :checked="targetChoices[target.id]?.[phase.key]" :disabled="gitOnly || !targetChoices[target.id]?.selected || !target.steps[phase.key]" @change="setTargetPhase(target.id, phase.key, ($event.target as HTMLInputElement).checked)" /><span>{{ phase.label }}<small>{{ targetPhaseHint(target, phase) }}</small></span></label></div>
                  <div v-if="!gitOnly && invalidChosenTargetIds.includes(target.id)" class="target-error">{{ tr("请至少选择一个有命令的动作；也可以修改配置或选择“仅 Git”。") }}</div>
                  <div v-if="target.steps.check" class="target-check">{{ tr("发布前会先自动检查") }}</div>
                </article>
              </div>
            </template>
            <div v-else-if="configEndpointAvailable" class="empty-config"><p>{{ tr("还没有配置 PC、Web、Android 或服务端等发布目标。") }}</p><button class="primary" :disabled="configScanning" @click="scanReleaseConfig">{{ configScanning ? tr("正在分析项目…") : tr("一键自动识别项目") }}</button></div>
          </section>

          <section class="block"><h3>{{ tr("Git 与检查设置") }}</h3><div class="form-grid"><label>{{ tr("远程仓库") }}<select v-model="remoteName"><option v-for="remote in preflight.remotes" :key="remote" :value="remote">{{ remote }}</option></select></label><label>{{ tr("版本文件识别") }}<select v-model="versionStrategy"><option value="auto">{{ tr("自动识别") }}</option><option value="tauri">Tauri</option><option value="node">Node</option><option value="manual">{{ tr("不自动修改") }}</option></select></label></div><details class="advanced compact"><summary>{{ tr("高级：通用发布前检查命令") }}</summary><label class="full-label">{{ tr("命令（可选）") }}<input v-model="preReleaseCommand" :placeholder="tr('例如 npm test')" /></label></details><button @click="saveAndRecheck" :disabled="savingProfile">{{ savingProfile ? tr("检查中…") : tr("保存并重新检查 Git") }}</button></section>

          <section class="block">
            <h3>{{ tr("版本与提交详情") }}</h3>
            <template v-if="createTag"><div class="strategy-line">{{ tr("将更新") }} {{ selectedVersionFiles.join('、') || tr("不修改版本文件") }}</div><div v-if="Object.keys(visibleCurrentVersions).length" class="current-versions"><code v-for="(version, file) in visibleCurrentVersions" :key="file">{{ file }}: {{ version || tr("未识别") }}</code></div><div v-for="version in plannedVersions" :key="version.versionGroupId" class="kv"><span>{{ version.versionGroupName }}</span><code>{{ version.tagName }}</code></div></template>
            <div v-else class="no-tag-note">{{ tr("本次不会修改版本文件，也不会创建或推送 Tag。") }}</div>
            <label class="full-label">{{ tr("提交说明") }}<input v-model="commitMessage" @input="onCommitMessageInput" /></label>
          </section>
            </div>
          </details>

          <section class="file-picker">
            <div class="section-head file-picker-head">
              <h3>{{ tr("选择提交文件") }}</h3>
              <div v-if="preflight.changes.length" class="file-actions">
                <span>{{ tr("已选") }} {{ selectedPaths.length }} / {{ preflight.changes.length }}</span>
                <button type="button" @click="selectAllFiles(!allFilesSelected)">{{ allFilesSelected ? tr("取消全选") : tr("全选") }}</button>
              </div>
            </div>
            <div v-if="unselectedNewFiles.length" class="alert warn file-warning">{{ tr('还有 {0} 个新增文件未勾选，发布后远端不会包含这些文件。', [unselectedNewFiles.length]) }}</div>
            <div v-if="!preflight.changes.length" class="muted">{{ tr("当前没有代码变更。创建 Tag 时，版本文件仍会自动更新并提交。") }}</div>
            <div v-else class="file-list">
              <label v-for="file in orderedChanges" :key="file.path" class="file-row" :class="{ unselected: !selected[file.path] }">
                <input v-model="selected[file.path]" type="checkbox" :disabled="file.staged" />
                <span class="file-status" :class="{ added: !file.tracked }">{{ fileStatusLabel(file) }}</span>
                <code :title="file.path">{{ file.path }}</code>
              </label>
            </div>
            <div class="file-footnote">{{ tr("版本设置自动修改的版本文件会一并提交，不受这里的勾选影响。") }}</div>
            <details v-if="preflight.aheadCount" class="unpushed-files">
              <summary>{{ tr('已提交到本机，等待上传 {0}（{1} 次提交，{2} 个文件）', [remoteDestination, preflight.aheadCount, preflight.unpushedChanges.length]) }}</summary>
              <div class="unpushed-note">{{ tr("这些文件已经提交，所以不会出现在上面的待提交列表中。") }}</div>
              <div class="file-list committed-list">
                <div v-for="file in preflight.unpushedChanges" :key="file.path" class="file-row committed-row">
                  <span class="committed-mark">✓</span>
                  <span class="file-status" :class="{ added: file.status.startsWith('A') }">{{ file.status.startsWith('A') ? tr("新增") : file.status.startsWith('D') ? tr("删除") : file.status.startsWith('R') ? tr("改名") : tr("修改") }}</span>
                  <code :title="file.path">{{ file.path }}</code>
                </div>
              </div>
            </details>
          </section>

          <section v-if="createTag" class="release-notes">
            <div class="release-notes-head">
              <h3>{{ tr("更新说明（将显示在 GitHub）") }}</h3>
              <button type="button" :disabled="releaseNotesLoading" @click="generateReleaseNotesDraft(true)">{{ releaseNotesLoading ? tr("生成中…") : tr("重新生成") }}</button>
            </div>
            <textarea
              v-model="releaseNotes"
              rows="7"
              maxlength="12000"
              :placeholder="releaseNotesLoading ? tr('正在自动生成，也可以直接填写…') : tr('请简要填写本次功能、问题修复和性能变化')"
              :aria-label="tr('更新说明')"
              @input="onReleaseNotesInput"
            ></textarea>
            <div class="release-notes-meta">
              <span v-if="releaseNotesLoading">{{ tr("正在根据提交记录生成初稿…") }}</span>
              <span v-else-if="releaseNotesDirty">{{ tr("已人工修改") }}</span>
              <span v-else-if="releaseNotes">{{ tr("已预留功能、修复和性能分类，可直接发布或修改") }}</span>
              <span v-if="releaseNotesBaseTag" :title="tr('基于 {0} 之后的变更生成', [releaseNotesBaseTag])">{{ tr("基于") }} {{ releaseNotesBaseTag }}</span>
            </div>
            <div v-if="releaseNotesStale" class="alert warn release-notes-alert">{{ tr("文件、构建端或版本已变化。现有内容不会被覆盖，可直接修改或重新生成。") }}</div>
            <div v-if="releaseNotesError" class="alert error release-notes-alert">{{ tr("生成失败：") }}{{ releaseNotesError }} <button type="button" @click="generateReleaseNotesDraft(true)">{{ tr("重试") }}</button></div>
            <div v-else-if="!releaseNotesLoading && !releaseNotes.trim()" class="field-error">{{ tr("创建 Tag 前请填写更新说明。") }}</div>
          </section>

          <section class="summary-card">
            <h3>{{ tr("本次操作") }}</h3>
            <div v-if="cloudExecutionNotice" class="cloud-execution-notice" role="note"><strong>{{ cloudExecutionNotice.title }}</strong><p>{{ cloudExecutionNotice.text }}</p></div>
            <p class="file-count">{{ tr('已选文件：{0} 个', [selectedPaths.length]) }}</p>
            <ul><li v-for="line in summaryLines" :key="line">{{ line }}</li></ul>
            <div v-if="hasOnlineAction" class="alert warn">{{ tr("包含上传或上线，请确认目标环境。") }}</div>
            <div v-if="automationBranchMismatch" class="alert warn">{{ tr('自动发布只接受 {0} 分支，当前为 {1}。', [configuredAutomation?.releaseBranch, preflight.branch]) }}</div>
            <div v-else-if="!createTag && automationTargetRequiresTag" class="alert warn">{{ tr("所选云端构建由 Tag 触发，请开启“创建版本 Tag”。") }}</div>
            <div v-else-if="!pushRemote && selectedNeedsRemotePush" class="alert warn">{{ tr("云端构建必须上传到 GitHub。") }}</div>
            <div v-else-if="invalidChosenTargetIds.length" class="alert warn">{{ tr("请为高级目标选择操作，或改为“仅提交代码”。") }}</div>
            <div v-else-if="!gitOnly && !selectedTargets.length" class="alert warn">{{ tr("请选择发布平台或“仅提交代码”。") }}</div>
          </section>
          <details v-if="history.length" class="history-panel"><summary>{{ tr('最近发布（{0}）', [history.length]) }}</summary><button v-for="run in history" :key="run.id" type="button" class="history-row" :aria-label="tr('查看 {0} 的发布记录', [run.tagName || tr('代码提交')])" @click="showRun(run)"><code>{{ run.createTag === false ? tr("无 Tag") : (run.versions?.map(version => version.tagName).join('、') || run.tagName) }}</code><span>{{ run.branch }}</span><span :class="run.status">{{ historyStatus(run) }} {{ tr("· 查看日志") }}</span></button></details>
          </template>
          <div v-else class="state panel-detail-loading">{{ tr("正在读取版本和代码变更…") }}</div>
        </template>
      </div>

      <footer v-if="preflight && !activeRun" class="m-foot" :inert="publishing">
        <span v-if="releaseContentHint" id="release-content-hint" class="release-content-hint" role="status">{{ releaseContentHint }}</span>
        <button :disabled="publishing" @click="emit('close')">{{ tr("取消") }}</button>
        <div ref="publishControlRef" class="publish-control">
          <div class="publish-button-group" :class="{ required: !pushRemote && selectedNeedsRemotePush }">
            <button class="primary publish-submit" :disabled="!canPublish" @click="publish()">{{ checkingRemote ? tr("正在检查 Git…") : publishing ? tr("正在创建…") : createTag ? (plannedVersions.length > 1 ? tr("确认发布 {0} 个版本", [plannedVersions.length]) : tr("确认发布 {0}", [plannedTagNames[0] || ''])) : tr("确认提交并执行") }}</button>
            <button type="button" class="publish-menu-trigger" :class="{ open: pushMenuOpen }" :aria-label="tr('发布选项')" :aria-expanded="pushMenuOpen" aria-haspopup="menu" @click="pushMenuOpen = !pushMenuOpen">▾</button>
          </div>
          <div v-if="pushMenuOpen" class="publish-menu" role="menu">
            <label class="publish-menu-option">
              <input v-model="pushRemote" type="checkbox" @change="pushMenuOpen = false" />
              <span class="publish-menu-check" aria-hidden="true">{{ pushRemote ? '✓' : '' }}</span>
              <span>{{ tr("上传") }} {{ remoteDestination }}</span>
            </label>
          </div>
        </div>
      </footer>
      <datalist id="target-kinds"><option value="desktop" /><option value="web" /><option value="android" /><option value="server" /><option value="custom" /></datalist><datalist id="runner-types"><option value="local" /><option value="git-push" /></datalist><datalist id="version-formats"><option value="json" /><option value="npm-lock" /><option value="cargo" /><option value="cargo-lock" /><option value="toml" /><option value="gradle" /></datalist>
      <div v-if="publishing" class="submitting-lock" role="status"><div class="submitting-message"><span class="submitting-spinner"></span><strong>{{ tr("正在创建发布任务…") }}</strong><p v-if="cloudExecutionNotice">{{ cloudExecutionNotice.text }}</p></div></div>
    </div>
    <div v-if="confirmAction" class="action-confirm-overlay" role="dialog" aria-modal="true" :aria-label="confirmDialogTitle" @click.self="confirmAction = null">
      <section class="action-confirm">
        <h3>{{ confirmDialogTitle }}</h3>
        <p>{{ confirmDialogMessage }}</p>
        <div class="action-confirm-buttons"><button @click="confirmAction = null">{{ tr("返回检查") }}</button><button class="primary" @click="confirmSensitiveAction">{{ confirmDialogButton }}</button></div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.m-foot { flex-wrap: wrap; align-items: center; }
.release-content-hint { margin-right: auto; flex: 1 1 180px; font-size: 12px; color: var(--text-dim); }
.overlay { position: fixed; inset: 0; z-index: 110; background: rgba(0,0,0,.58); display: flex; align-items: center; justify-content: center; padding: 20px; }.modal { width: min(920px,100%); max-height: 94vh; display: flex; flex-direction: column; background: var(--bg-elev); border: 1px solid var(--border); border-radius: 14px; box-shadow: var(--shadow); }.m-head { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 16px 20px; border-bottom: 1px solid var(--border); }.m-head h2 { margin: 0; font-size: 17px; }.m-body { padding: 18px 20px; overflow: auto; display: flex; flex-direction: column; gap: 16px; }.m-foot { padding: 14px 20px; border-top: 1px solid var(--border); display: flex; justify-content: flex-end; gap: 10px; }.state,.muted { color: var(--text-faint); font-size: 12px; }.alert { padding: 9px 11px; border-radius: 7px; font-size: 12px; line-height: 1.5; }.alert.error { color: var(--red); background: rgba(248,113,113,.10); border: 1px solid rgba(248,113,113,.3); }.alert.warn { color: var(--amber); background: rgba(251,191,36,.08); }.alert.info { color: var(--accent); background: rgba(79,140,255,.08); border: 1px solid rgba(79,140,255,.2); }
.repo-glance { display: flex; align-items: center; gap: 10px; min-width: 0; padding: 9px 12px; border: 1px solid rgba(52,211,153,.22); border-radius: 9px; color: var(--text-dim); background: rgba(52,211,153,.05); font-size: 12px; }.repo-glance strong { color: var(--text); }.repo-glance-status { margin-left: auto; color: var(--green); }.ready-dot { width: 8px; height: 8px; flex: 0 0 auto; border-radius: 50%; background: var(--green); }.repo-glance.checking { border-color: rgba(79,140,255,.3); background: rgba(79,140,255,.06); }.repo-glance.checking .ready-dot { background: var(--accent); animation: checking-pulse 1s ease-in-out infinite alternate; }.repo-glance.checking .repo-glance-status { color: var(--accent); }.repo-glance.problem { border-color: rgba(248,113,113,.25); background: rgba(248,113,113,.05); }.repo-glance.problem .ready-dot { background: var(--red); }.repo-glance.problem .repo-glance-status { color: var(--red); } @keyframes checking-pulse { to { opacity: .35; transform: scale(.75); } }
.platform-section { padding: 14px; border: 1px solid var(--border); border-radius: 11px; background: rgba(15,17,21,.36); }.platform-section h3 { margin: 0 0 4px; color: var(--text); font-size: 15px; }.basic-section-head { align-items: center; }.platform-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 9px; margin-top: 12px; }.platform-card { position: relative; display: grid; grid-template-columns: auto minmax(0,1fr); align-items: center; gap: 10px; min-height: 76px; padding: 12px; overflow: hidden; text-align: left; border: 1px solid var(--border); border-radius: 10px; color: var(--text); background: var(--bg); }.platform-card:not(:disabled):hover { border-color: rgba(79,140,255,.65); }.platform-card.selected { border-color: var(--accent); background: rgba(79,140,255,.1); box-shadow: inset 0 0 0 1px rgba(79,140,255,.16); }.platform-card.partial { border-color: var(--amber); border-style: dashed; }.platform-card.limited:not(.selected):not(.partial) { border-style: dashed; }.platform-card.unavailable { cursor: not-allowed; opacity: .62; }.platform-icon { font-size: 22px; line-height: 1; }.platform-copy { display: flex; min-width: 0; flex-direction: column; gap: 4px; padding-right: 14px; }.platform-copy strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }.platform-copy small { color: var(--text-faint); font-size: 10px; line-height: 1.35; }.chosen-mark { position: absolute; top: 8px; right: 9px; color: var(--accent); font-weight: 700; }.risk-badge { position: absolute; right: 8px; bottom: 6px; padding: 1px 5px; border-radius: 8px; color: var(--amber); background: rgba(251,191,36,.12); font-size: 9px; }.git-card .platform-icon { color: var(--accent); font-size: 26px; }
.file-picker { padding: 14px; border: 1px solid var(--border); border-radius: 11px; background: rgba(15,17,21,.36); }.file-picker h3 { margin: 0 0 4px; color: var(--text); font-size: 15px; }.file-picker-head { align-items: center; }.file-actions { display: flex; align-items: center; gap: 7px; flex-wrap: wrap; color: var(--text-faint); font-size: 11px; }.file-actions button { padding: 5px 8px; }.file-warning { margin-bottom: 9px; }.file-list { max-height: 230px; overflow: auto; padding: 3px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg); }.file-footnote { margin-top: 8px; color: var(--text-faint); font-size: 10px; }
.release-notes { padding: 14px; border: 1px solid var(--border); border-radius: 11px; background: rgba(15,17,21,.36); }.release-notes-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 9px; }.release-notes h3 { margin: 0; color: var(--text); font-size: 15px; }.release-notes-head button { flex: 0 0 auto; padding: 6px 9px; }.release-notes textarea { width: 100%; min-height: 132px; resize: vertical; line-height: 1.55; }.release-notes-meta { display: flex; min-height: 17px; align-items: center; justify-content: space-between; gap: 10px; margin-top: 6px; color: var(--text-faint); font-size: 10px; }.release-notes-alert { margin-top: 8px; }.release-notes-alert button { margin-left: 6px; padding: 3px 7px; }.automation-result { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 9px; }.automation-result a { flex: 0 0 auto; color: var(--accent); }
.unpushed-files { margin-top: 10px; border-top: 1px solid var(--border); padding-top: 10px; }.unpushed-files summary { cursor: pointer; color: var(--accent); font-size: 12px; }.unpushed-note { margin: 7px 0; color: var(--text-faint); font-size: 11px; }.committed-list { max-height: 180px; }.committed-row { grid-template-columns: auto 54px minmax(0,1fr); }.committed-mark { color: var(--green); }
.version-quick { padding: 14px; border: 1px solid var(--border); border-radius: 10px; }.block.version-quick h3 { margin: 0; color: var(--text); font-size: 15px; text-transform: none; letter-spacing: normal; }.version-list { display: flex; flex-direction: column; gap: 7px; }.version-row { display: grid; grid-template-columns: minmax(120px,1fr) minmax(100px,150px) minmax(150px,1fr); align-items: center; gap: 10px; padding: 9px 11px; border-radius: 8px; background: var(--bg); font-size: 12px; }.version-name { display: flex; min-width: 0; flex-direction: column; gap: 2px; }.version-name strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text); }.version-name small { color: var(--text-faint); }.version-next { color: var(--text-faint); }.version-next strong { margin-left: 5px; color: var(--accent); font-size: 14px; }.version-row code { overflow: hidden; text-overflow: ellipsis; color: var(--text-dim); white-space: nowrap; }.advanced-settings,.history-panel { border: 1px solid var(--border); border-radius: 10px; background: rgba(15,17,21,.26); }.advanced-settings > summary,.history-panel > summary { display: flex; align-items: center; gap: 9px; padding: 11px 13px; cursor: pointer; color: var(--text-dim); font-size: 12px; }.advanced-settings > summary small { color: var(--text-faint); font-size: 10px; }.advanced-settings[open] > summary,.history-panel[open] > summary { border-bottom: 1px solid var(--border); }.advanced-body { display: flex; flex-direction: column; gap: 16px; padding: 13px; }.history-panel { padding-bottom: 5px; }.history-panel .history-row { margin: 0 12px; }.file-count { font-weight: 600; color: var(--text)!important; }
.repo-card { padding: 10px 12px; border: 1px solid var(--border); border-radius: 9px; background: var(--bg); }.kv { display: grid; grid-template-columns: 80px minmax(0,1fr); gap: 8px; padding: 3px 0; font-size: 12px; }.kv span { color: var(--text-faint); }.kv code { overflow: hidden; text-overflow: ellipsis; color: var(--text-dim); }.block h3,.config-section h3,.summary-card h3 { margin: 0 0 10px; font-size: 12px; color: var(--text-faint); text-transform: uppercase; letter-spacing: .05em; }.section-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 10px; }.section-head h3 { margin-bottom: 4px; }.section-help { color: var(--text-faint); font-size: 12px; }.toolbar,.editor-actions { display: flex; gap: 8px; flex-wrap: wrap; }.config-section { padding: 13px; border: 1px solid var(--border); border-radius: 10px; background: rgba(15,17,21,.45); }.config-meta { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin: 8px 0; color: var(--text-faint); font-size: 11px; }.config-meta code { margin-left: auto; }.wizard-banner { display: flex; flex-direction: column; gap: 3px; padding: 11px; margin: 12px 0; border-radius: 8px; color: var(--text-dim); background: rgba(79,140,255,.08); }.wizard-banner span { font-size: 12px; }
.editor-subhead { display: flex; justify-content: space-between; align-items: center; margin: 14px 0 8px; color: var(--text); font-size: 13px; }.edit-card { padding: 11px; margin-bottom: 8px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg); }.target-edit-card { border-left: 3px solid var(--accent); }.target-edit-title { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; }.form-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 10px; }.form-grid.three { grid-template-columns: repeat(3,minmax(0,1fr)); }.form-grid label,.full-label { display: flex; flex-direction: column; gap: 5px; color: var(--text-dim); font-size: 12px; }.full-label { margin: 10px 0; }.form-grid input,.form-grid select,.full-label input,.full-label textarea { width: 100%; }.field-action { display: flex; align-items: flex-end; }.plain-check { display: inline-flex; align-items: center; gap: 6px; color: var(--text-dim); font-size: 12px; }.os-row { display: flex; gap: 13px; align-items: center; margin-top: 11px; color: var(--text-faint); font-size: 12px; }.advanced { margin-top: 10px; border-top: 1px dashed var(--border); padding-top: 9px; }.advanced.compact { margin-bottom: 10px; }.advanced summary { cursor: pointer; color: var(--text-faint); font-size: 12px; }.version-file-row { display: grid; grid-template-columns: minmax(0,2fr) 100px minmax(0,1fr) auto; gap: 7px; margin: 8px 0; }.danger-text { color: var(--red); }.editor-actions { justify-content: flex-end; margin-top: 14px; }
.empty-config { padding: 18px; text-align: center; color: var(--text-faint); border: 1px dashed var(--border); border-radius: 8px; font-size: 12px; }.setup-callout { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px; margin: 10px 0; border-radius: 8px; background: rgba(251,191,36,.08); color: var(--amber); font-size: 12px; }.git-only-choice { display: flex; align-items: center; gap: 7px; margin: 10px 0; color: var(--text-dim); font-size: 12px; }.target-list { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 9px; margin-top: 10px; }.target-card { padding: 11px; border: 1px solid var(--border); border-radius: 9px; background: var(--bg); }.target-card.selected { border-color: rgba(79,140,255,.65); }.target-card.invalid { border-color: rgba(248,113,113,.7); }.target-card.disabled { opacity: .65; }.target-head { display: flex; justify-content: space-between; gap: 8px; align-items: flex-start; min-width: 0; }.target-select { display: flex; gap: 8px; min-width: 0; }.target-select span { display: flex; flex-direction: column; min-width: 0; }.target-select strong { color: var(--text); font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.target-select small { margin-top: 2px; color: var(--text-faint); font-size: 10px; }.detected-badge { flex-shrink: 0; color: var(--accent); font-size: 10px; }.unavailable { margin-top: 9px; color: var(--amber); font-size: 11px; }.phase-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 5px; margin-top: 10px; }.phase-choice { display: flex; gap: 6px; align-items: flex-start; padding: 6px; border-radius: 6px; background: var(--bg-elev); }.phase-choice span { display: flex; flex-direction: column; color: var(--text-dim); font-size: 11px; }.phase-choice small { color: var(--text-faint); font-size: 9px; }.phase-choice.unavailable { opacity: .45; }.phase-choice.risky { background: rgba(251,191,36,.1); }.target-error { margin-top: 7px; color: var(--red); font-size: 10px; }.target-check { margin-top: 7px; color: var(--green); font-size: 10px; }
.tag-switch-row { display: flex; justify-content: space-between; align-items: center; gap: 16px; }.tag-switch-row h3 { margin-bottom: 4px; }.tag-switch-row p { margin: 0; color: var(--text-faint); font-size: 12px; }.switch { position: relative; display: inline-flex; flex-shrink: 0; }.switch input { position: absolute; opacity: 0; }.switch span { width: 42px; height: 23px; border-radius: 20px; background: var(--border); transition: .15s; }.switch span::after { content: ''; display: block; width: 17px; height: 17px; margin: 3px; border-radius: 50%; background: #fff; transition: .15s; }.switch input:checked + span { background: var(--accent); }.switch input:checked + span::after { transform: translateX(19px); }.strategy-line,.current-versions { color: var(--text-dim); font-size: 12px; margin: 10px 0 9px; }.current-versions { display: flex; flex-wrap: wrap; gap: 7px; }.current-versions code { padding: 2px 5px; background: var(--bg); border-radius: 4px; }.mode-picker { display: flex; gap: 8px; margin: 10px 0; }.mode-picker label { display: flex; align-items: center; gap: 6px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; color: var(--text-dim); font-size: 12px; }.mode-picker label.active { border-color: var(--accent); background: rgba(79,140,255,.08); }.mode-picker small { color: var(--text-faint); }.no-tag-note { margin-top: 10px; padding: 10px; border-radius: 8px; color: var(--text-dim); background: var(--bg); font-size: 12px; }.invalid { border-color: var(--red)!important; }.field-error { margin-top: 5px; color: var(--red); font-size: 11px; }
.run-targets { display: flex; flex-direction: column; gap: 5px; margin: 8px 0; }.run-target { display: grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr) auto; gap: 8px; padding: 7px 9px; border-radius: 6px; background: var(--bg); font-size: 11px; }.run-target span { color: var(--text-faint); }.run-target em { font-style: normal; color: var(--text-dim); }.run-target em.succeeded { color: var(--green); }.run-target em.triggered,.run-target em.remote_pending,.run-target em.handed_off { color: var(--accent); }.run-target em.failed { color: var(--red); }.artifacts { margin: 9px 0; color: var(--text-dim); font-size: 11px; }.artifact-row { display: grid; grid-template-columns: minmax(0,1fr) auto 90px; gap: 8px; padding: 5px 0; }.artifact-row code { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.action-confirm-overlay { position: fixed; inset: 0; z-index: 120; display: flex; align-items: center; justify-content: center; padding: 20px; background: rgba(0,0,0,.68); }.action-confirm { width: min(430px,100%); padding: 20px; border: 1px solid var(--border); border-radius: 12px; background: var(--bg-elev); box-shadow: var(--shadow); }.action-confirm h3 { margin: 0 0 9px; color: var(--text); font-size: 16px; }.action-confirm p { margin: 0; color: var(--text-dim); font-size: 13px; line-height: 1.6; }.action-confirm-buttons { display: flex; justify-content: flex-end; gap: 9px; margin-top: 18px; }
.publish-control { position: relative; display: inline-flex; }.publish-button-group { display: inline-flex; overflow: hidden; border-radius: 7px; box-shadow: 0 0 0 1px rgba(79,140,255,.38); }.publish-submit { border-radius: 7px 0 0 7px; }.publish-menu-trigger { width: 34px; padding: 0; border: 0; border-left: 1px solid rgba(255,255,255,.22); border-radius: 0 7px 7px 0; color: rgba(255,255,255,.72); background: var(--accent); font-size: 12px; }.publish-menu-trigger:hover:not(:disabled),.publish-menu-trigger.open { color: #fff; background: var(--accent-hover); }.publish-button-group.required { box-shadow: 0 0 0 1px rgba(251,191,36,.72); }.publish-button-group.required .publish-menu-trigger { color: var(--amber); background: rgba(251,191,36,.14); }.publish-menu { position: absolute; z-index: 4; right: 0; bottom: calc(100% + 8px); min-width: 176px; padding: 5px; border: 1px solid var(--border); border-radius: 8px; background: var(--bg-elev-2); box-shadow: var(--shadow); }.publish-menu-option { display: flex; align-items: center; gap: 9px; padding: 9px 10px; cursor: pointer; border-radius: 6px; color: var(--text); font-size: 12px; white-space: nowrap; user-select: none; }.publish-menu-option:hover { background: rgba(79,140,255,.1); }.publish-menu-option input { position: absolute; opacity: 0; pointer-events: none; }.publish-menu-option:has(input:focus-visible) { outline: 2px solid var(--accent); }.publish-menu-check { display: grid; width: 16px; height: 16px; place-items: center; border: 1px solid var(--text-faint); border-radius: 4px; color: var(--accent); font-size: 11px; line-height: 1; }.publish-menu-option input:checked + .publish-menu-check { border-color: var(--accent); background: rgba(79,140,255,.13); }
.modal { position: relative; }.submitting-lock { position: absolute; inset: 0; z-index: 115; display: flex; align-items: center; justify-content: center; gap: 10px; border-radius: inherit; color: var(--text); background: rgba(11,14,20,.78); backdrop-filter: blur(2px); }.submitting-spinner { width: 18px; height: 18px; border: 2px solid rgba(79,140,255,.25); border-top-color: var(--accent); border-radius: 50%; animation: submitting-spin .7s linear infinite; } @keyframes submitting-spin { to { transform: rotate(360deg); } }
.file-row { display: grid; grid-template-columns: auto 54px minmax(0,1fr); align-items: center; gap: 8px; min-height: 32px; color: var(--text-dim); font-size: 12px; }.file-row + .file-row { border-top: 1px solid rgba(148,163,184,.08); }.file-row.unselected { opacity: .62; }.file-row code { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.file-status { color: var(--amber); font-size: 11px; }.file-status.added { color: var(--accent); }.summary-card { padding: 13px; border: 1px solid rgba(79,140,255,.35); border-radius: 10px; background: rgba(79,140,255,.06); }.summary-card p { margin: 0 0 6px; color: var(--text-dim); font-size: 12px; }.summary-card ul { margin: 0 0 10px; padding-left: 20px; color: var(--text); font-size: 12px; line-height: 1.8; }.history-row { display: grid; grid-template-columns: 100px 1fr auto; gap: 10px; padding: 5px 0; border-bottom: 1px solid var(--border); font-size: 12px; }.history-row span { color: var(--text-faint); }.history-row .succeeded,.status.succeeded { color: var(--green); }.history-row .failed,.status.failed { color: var(--red); }.progress-title { display: flex; align-items: center; justify-content: space-between; }.status { font-size: 12px; }.current-stage { margin: 8px 0; color: var(--accent); font-size: 13px; }.log-box { min-height: 180px; max-height: 320px; overflow: auto; padding: 10px; border-radius: 8px; background: #070b11; color: #cbd5e1; font: 12px/1.55 Consolas,monospace; white-space: pre-wrap; }.log-line.error { color: #fca5a5; }.log-line.stderr { color: #fbbf24; }.button-row { display: flex; gap: 8px; margin-top: 12px; }
@media (max-width: 720px) { .overlay { padding: 8px; }.modal { max-height: 97vh; }.m-head > div { min-width: 0; }.m-head h2 { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }.m-body { padding: 14px; }.repo-glance { flex-wrap: wrap; }.repo-glance-status { margin-left: 0; width: 100%; padding-left: 18px; }.section-head,.setup-callout { flex-direction: column; align-items: stretch; }.platform-grid { grid-template-columns: repeat(2,minmax(0,1fr)); }.form-grid,.form-grid.three,.target-list { grid-template-columns: 1fr; }.version-row { grid-template-columns: minmax(0,1fr) minmax(100px,140px); }.version-row code { grid-column: 1 / -1; }.version-file-row { grid-template-columns: 1fr; }.file-actions { align-items: stretch; }.file-row { grid-template-columns: auto 48px minmax(0,1fr); }.os-row,.mode-picker { flex-wrap: wrap; }.mode-picker label { flex: 1 1 160px; }.run-target { grid-template-columns: minmax(0,1fr) auto; }.run-target span { grid-column: 1 / -1; grid-row: 2; }.artifact-row { grid-template-columns: minmax(0,1fr) auto; }.artifact-row code:last-child { grid-column: 1 / -1; }.release-notes-head,.release-notes-meta,.automation-result { align-items: flex-start; flex-direction: column; }.automation-result a { align-self: flex-start; } }
@media (max-width: 440px) { .overlay { padding: 0; }.modal { width: 100%; max-height: 100vh; border-radius: 0; }.m-head,.m-foot { padding-left: 14px; padding-right: 14px; }.platform-grid { grid-template-columns: 1fr; }.platform-card { min-height: 68px; }.m-foot > button { flex: 0 0 auto; }.publish-control,.publish-button-group { flex: 1; min-width: 0; }.publish-submit { flex: 1; min-width: 0; }.history-row { grid-template-columns: 85px minmax(0,1fr) auto; } }
.completion-banner { display: flex; flex-direction: column; align-items: center; gap: 12px; margin-bottom: 24px; padding: 30px 24px; border: 1px solid rgba(52,211,153,.5); border-radius: 14px; background: linear-gradient(145deg,rgba(52,211,153,.13),rgba(52,211,153,.035)); text-align: center; }
.completion-icon { display: grid; place-items: center; width: 60px; height: 60px; border-radius: 50%; background: rgba(52,211,153,.16); color: var(--green); font-size: 36px; font-weight: 700; line-height: 1; }
.completion-banner h3 { margin: 0; color: var(--green); font-size: 28px; line-height: 1.3; }
.completion-banner p { margin: 0; color: var(--text); font-size: 15px; }
.completion-next { display: flex; flex-direction: column; gap: 8px; width: 100%; margin-top: 6px; padding-top: 18px; border-top: 1px solid rgba(52,211,153,.2); color: var(--text-dim); font-size: 13px; line-height: 1.6; }
.completion-next strong { color: var(--text); font-size: 16px; }
.completion-next .cloud-result-pending { color: var(--text-dim); font-size: 12px; }
.actions-link { display: inline-flex; align-items: center; justify-content: center; gap: 10px; margin-top: 4px; padding: 11px 18px; border-radius: 8px; background: var(--accent); color: #fff; text-decoration: none; font-size: 14px; font-weight: 600; }
.actions-link:hover { background: var(--accent-hover); }
.actions-link:focus-visible { outline: 2px solid var(--text); outline-offset: 3px; }
.cloud-execution-notice { padding: 15px 16px; margin-bottom: 16px; border: 1px solid rgba(79,140,255,.4); border-left: 4px solid var(--accent); border-radius: 8px; background: rgba(79,140,255,.1); }
.cloud-execution-notice strong { display: block; color: var(--text); font-size: 15px; }
.cloud-execution-notice p { margin: 7px 0 0; color: var(--text-dim); font-size: 13px; line-height: 1.65; }
.execution-details { margin: 12px 0; }
.history-row { width: 100%; text-align: left; background: transparent; border-radius: 0; cursor: pointer; }
.history-row:hover { background: rgba(79,140,255,.08); }
.execution-details > summary { padding: 7px 0; cursor: pointer; color: var(--text-dim); font-size: 12px; }
.submitting-message { display: flex; align-items: center; justify-content: center; flex-wrap: wrap; gap: 12px; max-width: 480px; padding: 24px; text-align: center; }
.submitting-message p { flex-basis: 100%; margin: 0; font-size: 14px; line-height: 1.7; color: var(--text-dim); }
.progress-title { gap: 12px; }
.progress-title > strong { min-width: 0; overflow-wrap: anywhere; }
.progress-title > .status { flex-shrink: 0; }
@media (max-width: 720px) { .completion-banner { padding: 24px 16px; gap: 10px; }.completion-banner h3 { font-size: 25px; }.completion-icon { width: 52px; height: 52px; font-size: 30px; }.actions-link { width: 100%; }.cloud-execution-notice { padding: 12px; } }
</style>
