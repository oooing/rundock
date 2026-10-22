<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { tr } from '@/i18n'
import { cloudFailureText, localFailureText } from '@/utils/buildFailure'
import { releaseTargetLabel } from '@/utils/releasePresentation'
import { releaseSubmissionFeedback } from '@/utils/releaseSubmissionFeedback'
import { artifactSizeSummary, formatArtifactSize, savedDeliveryReady } from '@/utils/releaseProgress'
import type { AppView, ReleaseRun } from '@/types'
import { useReleaseModel } from './release/useReleaseModel'
import ReleaseConfigFileEditor from './ReleaseConfigFileEditor.vue'
import ReleaseSafetyPanel from './ReleaseSafetyPanel.vue'
import ReleaseIgnoredFolders from './ReleaseIgnoredFolders.vue'
import ReleaseFormSection from './ReleaseFormSection.vue'
import ReleaseSafetySettings from './ReleaseSafetySettings.vue'
import CopyErrorButton from './CopyErrorButton.vue'
import ReleaseSyncChoice from './ReleaseSyncChoice.vue'
import ReleasePreferenceStatus from './ReleasePreferenceStatus.vue'
import ReleaseDeliveryStatus from './ReleaseDeliveryStatus.vue'
import ReleaseVersionChange from './ReleaseVersionChange.vue'
import LocalBuildPanel from './LocalBuildPanel.vue'
import ReleaseBuildChoice from './ReleaseBuildChoice.vue'
import ReleaseSetupOverview from './ReleaseSetupOverview.vue'
import SavedReleaseArtifacts from './SavedReleaseArtifacts.vue'
import ReleaseProgressOverview from './ReleaseProgressOverview.vue'
import ReleaseCancelButton from './ReleaseCancelButton.vue'
import ReleaseTargetProgress from './ReleaseTargetProgress.vue'
const props = defineProps<{ app: AppView }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const {
  phaseOptions, loading, savingProfile, publishing, autoSubmitting,
  unstaging, unstageNotice, error, errorCode, versionPlanNotice,
  preflight, history, selected, releaseIntent, manualDecisions,
  sensitiveExceptions, candidate, checkingCandidate, ignoringFile, pendingIgnoreCount, ignoreSafetyFile, ignoreSafetyFiles, updateManagedIgnores, checksEnabled, candidateSignature,
  reviewSignature, findingDecisions, resolvingReview, safetySettingsDirty, candidatePoll,
  candidateEpoch, candidateAbort, safetyFiles, candidateRequest, currentCandidateSignature,
  submissionPlanSignature, candidateStale, reviewStale, pendingFindings, candidateReady,
  candidateNeedsAttention, showReleaseIssues, chooseSafetyFile, adoptRecommended, recordSensitiveException,
  continueResolvedReview, changeReleaseIntent, refreshSafety, inspectCandidate, cancelCandidate,
  saveSafetyConfig, versionInputs, commitMessage, commitMessageDirty, releaseNotes,
  releaseNotesDirty, releaseNotesStale, releaseNotesLoading, releaseNotesError, releaseNotesBaseTag,
  releaseNotesSourceFingerprint, releaseNotesGeneratedFor, remoteName, versionStrategy, preReleaseCommand,
  createTag, pushRemote, buildMode, versionMode, profileReady,
  preferenceStatus, preferenceError, preferenceRevision, editedReleaseOptions, releaseConfig,
  configDraft, configBeforeEdit, targetChoices, configEndpointAvailable, configNotice,
  configEditorOpen, configFileOpen, configScanning, configSaving, configValidationError,
  preflightStale, gitOnly, releaseTab, tabScroll, configFileDirty,
  confirmAction, bodyRef, platformSectionRef, activeRun, logs,
  runTargets, runArtifacts, runAutomation, runDeliveries, cloudBuild,
  retrying, openingAutomation, automationOpenError, retryMetadataLoaded, retryConfirmationRequired,
  retryConfirmationTargets, pollTimer, preferenceTimer, releaseNotesTimer, releaseNotesRequest,
  preferenceSave, disposed, isActive, selectedPaths, orderedChanges,
  newFiles, allFilesSelected, configuredTargets, chosenTargets, selectedTargets,
  invalidChosenTargetIds, targetSelectionMissing, selectedVersionGroupIds, selectedVersionGroups, selectedVersionFiles,
  visibleCurrentVersions, isTagPushTarget, plannedVersions, versionForGroup,
  platformVersions, displayCurrentVersion, versionPattern, versionValid, primaryTargetVersion,
  plannedTagNames, configNeedsSaving, selectedNeedsRemotePush, configuredAutomation, selectedHasTagPushTarget,
  automationTargetRequiresTag, willTriggerAutomation, automationBranchMismatch, willBuildWindowsInAutomation, willBuildTargetsInAutomation,
  releaseNotesOptionsSignature, targetSelectionValid, newContentState, releaseContentHint, blockingIssues,
  localChecksPassed, remoteMissing, canSubmit, canPublish, canRetryRun,
  retryable, customRetryConfirmation, retryUpload, uploadPaused, runFailureSummary,
  runFailureDetails, retryButtonLabel, retryGuidance, retryTargetNames, hasOnlineAction,
  hasExternalAction, remoteDestination, automationPageUrl, openAutomationPage, automationHandedOff,
  activeRunStatusLabel, activeStageLabel, cloudExecutionNotice, cloudBuildSettled, completionTitle,
  completionDescription, confirmDialogTitle, confirmDialogMessage, confirmDialogButton, configConfidence,
  standardPlatforms, platformIdForTarget, versionGroupDisplayName, productPlatforms, visibleProductPlatforms,
  phaseAllowed, selectedDelivery, syncPolicy, syncRepository, syncNotice, syncDeliveryMissing, changeSyncPolicy, changeBuildMode, configuredActions,
  localVersionMode, localBuildOnly, changeLocalVersionMode, buildPlan, changeBuildPlan,
  platformRunnableTargets, platformSelected, platformHasSelection, platformPartiallySelected, platformPartiallyAvailable,
  platformSelectionCount, platformUnavailableReason, platformActionLabels, platformCardDetail, togglePlatform,
  selectSingleBuildPlatform, toggleGitOnly, stageLabel, targetStageLabel, summaryLines,
  messageOf, githubRepositoryUrl, githubActionsUrl, nextPatchVersion, suggestReleaseVersion,
  cloneConfig, normalizeConfig, currentOS, osLabel, targetUnavailableReason,
  targetPhaseHint, targetAvailable, defaultTargetChoice, applyReleaseConfig, resetSelection,
  fileStatusLabel, isAddedFile, selectAllFiles, normalizePreflight, preferenceKey,
  readLocalPreferences, rememberPreferences, saveRememberedPreferences, closeModal, setDefaultCommitMessage,
  onCommitMessageInput, syncVersionInputs, scheduleReleaseNotesDraft, generateReleaseNotesDraft, onReleaseNotesInput,
  applyPreflight, unstageFiles, load, profileBody, saveAndRecheck,
  onVersionInput, onCreateTagChange, onVersionModeChange, setTargetSelected, setTargetPhase,
  versionGroupName, scanReleaseConfig, openConfigEditor, cancelConfigEdit, validateConfig,
  saveReleaseConfig, switchReleaseTab, chooseReleaseTarget, onReleaseTabKeydown, onConfigFileSaved,
  newId, addVersionGroup, removeVersionGroup, addVersionFile, addTarget,
  setRunnerOS, setArtifacts, submitRelease, publish, prepareLocalCommit,
  releaseErrorMessage, showRun, leaveRunView, historyStatus, schedulePoll, poll,
  retry, confirmSensitiveAction, startNew, preparation, preparationHistory, restorePreparation, submissionUncertain, runConnectionError,
} = useReleaseModel(props, emit)
const advancedSettings = ref<HTMLDetailsElement | null>(null)
async function openAdvancedSettings() {
  if (advancedSettings.value) advancedSettings.value.open = true
  await nextTick()
  advancedSettings.value?.querySelector('summary')?.focus()
  advancedSettings.value?.scrollIntoView({ block: 'nearest' })
}
const localBuildMounted = ref(false)
const localBuildVisible = computed(() => profileReady.value && !loading.value && !activeRun.value && localBuildOnly.value)
// Keep the task controller alive when hiding the module or visiting Settings.
// Task creation/cancellation remains an explicit action, never a mode-change effect.
watch(localBuildVisible, visible => { if (visible) localBuildMounted.value = true }, { immediate: true })
const sealedArtifactsPresent = ref(false)
const hasLocalSavedOutputs = computed(() => !!activeRun.value
  && !['queued', 'running'].includes(activeRun.value.status)
  && runTargets.value.some(target => (target.build || target.package) && !target.publish))
watch(() => activeRun.value?.id, () => { sealedArtifactsPresent.value = false }, { flush: 'sync' })
const panelTab = computed(() => activeRun.value ? 'publish' : releaseTab.value)
const submissionFeedback = computed(() => releaseSubmissionFeedback({
  busy: autoSubmitting.value || checkingCandidate.value || publishing.value,
  error: error.value, notice: versionPlanNotice.value,
  needsAttention: candidateNeedsAttention.value, stale: !!candidate.value && reviewStale.value,
  ready: candidateReady.value && canSubmit.value, status: candidate.value?.status,
  formal: createTag.value && syncPolicy.value !== 'local',
}))
const connectionFailedBeforeBuild = computed(() => activeRun.value?.status === 'failed' && !activeRun.value.commitSha
  && ['delivery_connecting', 'delivery_preflight'].includes(activeRun.value.stage))
const savedOutputsReady = computed(() => !!activeRun.value && retryMetadataLoaded.value
  && savedDeliveryReady(activeRun.value, runDeliveries.value, runTargets.value))
const syncNeedsSetup = computed(() => syncDeliveryMissing.value || (syncPolicy.value === 'auto' && !!preflight.value && !syncRepository.value))
const publishActionLabel = computed(() => {
  if (syncPolicy.value === 'local') return tr('升级版本并本地打包')
  if (candidateReady.value && !autoSubmitting.value) return tr('确认发布')
  if (buildMode.value === 'local') return plannedVersions.value.length > 1
    ? tr('构建并发布 {0} 个版本', [plannedVersions.value.length]) : tr('构建并发布 {0}', [plannedTagNames.value[0] || ''])
  return plannedVersions.value.length > 1
    ? tr('云端构建并发布 {0} 个版本', [plannedVersions.value.length]) : tr('云端构建并发布 {0}', [plannedTagNames.value[0] || ''])
})
const closeLocked = computed(() => publishing.value || autoSubmitting.value || checkingCandidate.value || retrying.value
  || !!activeRun.value && ['queued', 'running'].includes(activeRun.value.status))
function closePanel() {
  if (closeLocked.value) return
  if (localBuildVisible.value && !preflight.value) emit('close')
  else void closeModal()
}
const historyOpen = ref(false)
const recoverablePreparations = computed(() => preparationHistory.value.filter(item => !item.runId && item.status !== 'dismissed'))
async function recoverPreparation(item: typeof preparation.value) {
  if (!item) return
  await restorePreparation(item)
  historyOpen.value = false
}
const historyTitle = ref<HTMLHeadingElement | null>(null)
const historyButton = ref<HTMLButtonElement | null>(null)
let draftScroll = 0, historyScroll = 0, draftError = ''
async function openHistory() {
  if (publishing.value || autoSubmitting.value || checkingCandidate.value || ignoringFile.value || activeRun.value) return
  draftScroll = bodyRef.value?.scrollTop || 0
  draftError = error.value
  historyScroll = 0
  historyOpen.value = true
  await nextTick()
  bodyRef.value?.scrollTo({top: 0})
  historyTitle.value?.focus({preventScroll: true})
}
async function openHistoryRun(run: ReleaseRun) {
  historyScroll = bodyRef.value?.scrollTop || 0
  showRun(run)
  await nextTick()
  bodyRef.value?.querySelector<HTMLElement>('.progress-block')?.focus({preventScroll: true})
}
async function returnToHistory() {
  if (retrying.value || publishing.value || autoSubmitting.value) return
  leaveRunView()
  error.value = draftError
  await nextTick()
  // The existing run watcher resets scrolling after the DOM update; restore after it.
  await nextTick()
  bodyRef.value?.scrollTo({top: historyScroll})
  historyTitle.value?.focus({preventScroll: true})
}
async function returnToRelease() {
  if (retrying.value || publishing.value || autoSubmitting.value) return
  if (activeRun.value) leaveRunView()
  error.value = draftError
  historyOpen.value = false
  await nextTick()
  await nextTick()
  bodyRef.value?.scrollTo({top: draftScroll})
  historyButton.value?.focus({preventScroll: true})
}
</script>

<template>
  <div class="overlay" @click.self="closePanel">
    <div class="modal">
      <header class="m-head" :class="{'history-navigation':historyOpen}">
        <button v-if="historyOpen || connectionFailedBeforeBuild" type="button" class="ghost history-back" :disabled="retrying || publishing || autoSubmitting" @click="historyOpen ? (activeRun ? returnToHistory() : returnToRelease()) : startNew()"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none"><path d="m15 5-7 7 7 7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg><span>{{historyOpen && activeRun?tr('返回历史'):tr('返回发布')}}</span></button>
        <h2 ref="historyTitle" :id="historyOpen ? 'release-history-title' : undefined" :tabindex="historyOpen ? -1 : undefined"><template v-if="historyOpen">{{activeRun?tr('发布记录'):tr('发布历史')}}</template><template v-else>{{ tr('发布') }} {{app.name}}</template></h2>
        <button class="ghost icon" :disabled="closeLocked || !localBuildVisible && preferenceStatus === 'saving'" :aria-label="tr('关闭')" @click="closePanel">✕</button>
      </header>

      <nav v-show="!historyOpen" class="release-tabs" role="tablist" :aria-label="tr('发布页面')" @keydown="!historyOpen && onReleaseTabKeydown($event)">
        <button id="release-tab-publish" type="button" role="tab" :aria-controls="historyOpen && !activeRun ? 'release-panel-history' : 'release-panel-publish'" :aria-selected="panelTab === 'publish'" :tabindex="panelTab === 'publish' ? 0 : -1" :disabled="loading || !profileReady || publishing || autoSubmitting || retrying" @click="historyOpen ? returnToRelease() : switchReleaseTab('publish')">{{ tr('发布') }}</button>
        <button id="release-tab-settings" type="button" role="tab" :aria-label="tr('设置')" aria-controls="release-panel-settings" :aria-selected="panelTab === 'settings'" :tabindex="panelTab === 'settings' ? 0 : -1" :disabled="loading || !profileReady || publishing || autoSubmitting || !!activeRun || historyOpen" @click="switchReleaseTab('settings')">{{ tr('设置') }}<span v-if="configFileDirty || configEditorOpen" class="unsaved-dot" :aria-label="tr('有未保存的修改')"></span></button>
      </nav>

      <ReleaseProgressOverview v-if="activeRun" :run="activeRun" :logs="logs" :targets="runTargets" :deliveries="runDeliveries" :definitions="configuredTargets" :artifacts="runArtifacts" :cloud-handoff="automationHandedOff" :cloud-build="cloudBuild" :connection-lost="!!runConnectionError" :pending-result="!retryMetadataLoaded">
        <template #actions><ReleaseCancelButton :key="activeRun.id" :run-id="activeRun.id" :status="activeRun.status" @refresh="poll" /></template>
      </ReleaseProgressOverview>

      <div ref="bodyRef" class="m-body" :inert="publishing || autoSubmitting">
        <div v-if="isActive && !activeRun" class="alert warn">{{ tr("项目正在运行；发布不会自动停止或重启。") }}</div>
        <div v-if="loading && !activeRun" class="release-loading" role="status" aria-live="polite" aria-busy="true">
          <progress class="release-loading-spinner" :aria-label="tr('读取项目信息…')"></progress>
          <strong>{{ tr('读取项目信息…') }}</strong>
          <span>{{ tr('读取设置、版本和文件，尚未开始发布检查。') }}</span>
        </div>
        <div v-if="error && !localBuildVisible && !activeRun" class="alert error" role="alert">{{ error }}</div>
        <div v-if="!loading && !profileReady && !activeRun" class="release-load-error"><button type="button" @click="load">{{ tr('重新加载') }}</button></div>
        <div v-if="preferenceStatus === 'error'" class="alert error" role="alert">{{ tr('设置保存失败，请重试。') }} {{ preferenceError }} <button type="button" @click="saveRememberedPreferences">{{ tr('重试保存') }}</button></div>
        <div v-if="versionPlanNotice && releaseIntent === 'formal' && !localBuildVisible" class="alert warn" role="status">{{ versionPlanNotice }}</div>
        <div v-if="preflightStale" class="alert warn">{{ tr('设置或代码状态已变化，请刷新项目信息。') }}<button :disabled="savingProfile" @click="saveAndRecheck">{{ tr('刷新信息') }}</button></div>

        <template v-if="activeRun">
          <section id="release-panel-publish" :role="historyOpen ? 'region' : 'tabpanel'" :aria-labelledby="historyOpen ? 'release-history-title' : 'release-tab-publish'" class="progress-block" tabindex="0">
            <div v-if="runConnectionError" class="alert warn" role="status">{{tr('连接中断，正在核对任务结果。后台任务可能仍在执行，请勿重复发布。')}}</div>
            <section v-if="activeRun.status === 'succeeded'" class="completion-banner" :class="{ pending: automationHandedOff && !cloudBuildSettled, failed: cloudBuild?.state === 'failed' }" role="status" aria-live="polite">
              <p>{{ completionDescription }}</p>
              <CopyErrorButton v-if="cloudBuild?.state === 'failed'" :text="cloudFailureText(cloudBuild)" />
              <p v-if="cloudBuild?.state === 'failed'">{{tr('打开失败任务，处理提示后重试；这里会自动更新。')}}</p>
              <template v-if="automationHandedOff">
                <div class="completion-next"><strong>{{ cloudExecutionNotice?.title || tr("后续由 GitHub Actions 执行") }}</strong><span>{{ cloudExecutionNotice?.text }}</span><span v-if="!cloudBuildSettled" class="cloud-result-pending">{{ tr('可以关闭此窗口；应用运行期间会继续跟踪，失败时提醒你。') }}</span></div>
                <button v-if="automationPageUrl" type="button" class="actions-link" :disabled="openingAutomation" :aria-busy="openingAutomation" @click="openAutomationPage">{{ openingAutomation ? tr('正在打开浏览器…') : cloudBuild?.state === 'failed' ? tr('查看失败') : tr("查看 GitHub Actions 进度") }} <span aria-hidden="true">↗</span></button>
                <p v-if="automationOpenError" class="field-error" role="alert">{{ automationOpenError }}</p>
              </template>
            </section>
            <div v-else-if="activeRun.status !== 'failed' && cloudExecutionNotice" class="cloud-execution-notice" role="note"><strong>{{ cloudExecutionNotice.title }}</strong><p>{{ cloudExecutionNotice.text }}</p></div>
            <div class="progress-title"><strong>{{ runTargets.length ? tr('平台进度') : tr('本次操作') }}</strong><span class="release-version">{{ activeRun.createTag === false ? tr("代码更新") : (activeRun.versions?.map(version => version.tagName).join('、') || activeRun.tagName) }}</span></div>
            <ReleaseTargetProgress v-if="runTargets.length" :targets="runTargets" :deliveries="runDeliveries" :definitions="configuredTargets" :cloud-build="cloudBuild" />
            <details v-if="runDeliveries.length" class="execution-details delivery-details" :open="activeRun.status === 'succeeded'"><summary>{{ tr('发布详情') }}</summary><ReleaseDeliveryStatus :run="activeRun" :deliveries="runDeliveries" :artifacts="runArtifacts" :definitions="configuredTargets" @refresh="poll" /></details>
            <details class="execution-details"><summary>{{ tr("执行日志") }}</summary><CopyErrorButton v-if="activeRun.status === 'failed'" :text="localFailureText(app.name, activeRun, runTargets, logs)" /><div class="log-box"><div v-for="line in logs" :key="line.id" :class="['log-line', line.stream]">{{ line.text }}</div><div v-if="!logs.length" class="muted">{{ tr("等待发布日志…") }}</div></div></details>
            <SavedReleaseArtifacts v-if="hasLocalSavedOutputs" :run-id="activeRun.id" @sealed="sealedArtifactsPresent = $event" />
            <details v-if="runArtifacts.length && !sealedArtifactsPresent" class="artifacts"><summary>{{ tr('已生成产物（{0}）', [runArtifacts.length]) }} · {{ tr('总大小：{0}', [artifactSizeSummary(runArtifacts).size]) }}<template v-if="artifactSizeSummary(runArtifacts).missing"> · {{ tr('{0} 个大小未知', [artifactSizeSummary(runArtifacts).missing]) }}</template></summary><div v-for="artifact in runArtifacts" :key="`${artifact.targetId}-${artifact.path}`" class="artifact-row"><code :title="artifact.path">{{ artifact.path }}</code><span>{{ formatArtifactSize(artifact.sizeBytes) }}</span><code>{{ artifact.sha256.slice(0, 12) }}</code></div></details>
            <p v-if="connectionFailedBeforeBuild" class="muted">{{ tr('尚未构建或上传。返回发布后可继续；内容未变时会保留已通过的检查。') }}</p>
            <details v-if="runFailureDetails" class="execution-details"><summary>{{ tr('查看技术详情') }}</summary><pre class="log-box">{{ runFailureDetails }}</pre></details>
            <details v-if="activeRun.commitSha" class="execution-details"><summary>{{ tr("代码提交") }}</summary><code>{{ activeRun.commitSha }}</code></details>
          </section>
        </template>

        <section v-if="!loading && profileReady" v-show="releaseTab === 'publish' && !historyOpen && !activeRun" :id="activeRun ? undefined : 'release-panel-publish'" role="tabpanel" aria-labelledby="release-tab-publish" class="release-panel" tabindex="0">
          <div v-if="recoverablePreparations.length && (!preparation || submissionUncertain) && !autoSubmitting && !checkingCandidate" class="alert info" role="status">
            {{submissionUncertain ? tr('上次提交的结果待确认。') : tr('有尚未发布的准备记录，可恢复选择后继续。')}}
            <button type="button" class="ghost" @click="recoverPreparation(preparation || recoverablePreparations[0])">{{submissionUncertain ? tr('核对结果') : tr('恢复准备')}}</button>
          </div>
          <ReleaseFormSection section-id="method" :title="releaseIntent === 'formal' ? tr('操作与发布方式') : tr('操作与保存位置')">
          <template #actions><ReleasePreferenceStatus :status="preferenceStatus" /></template>
          <fieldset class="publish-purpose" :disabled="checkingCandidate || publishing || autoSubmitting">
            <legend>{{ tr('本次目的') }}</legend>
            <div class="intent-choice purpose-options">
              <label :class="{ chosen: releaseIntent === 'formal' }"><input type="radio" name="release-intent" value="formal" :checked="releaseIntent === 'formal'" @change="changeReleaseIntent('formal')" /><span>{{ tr('发布版本') }}</span></label>
              <label :class="{ chosen: releaseIntent === 'save-progress' }"><input type="radio" name="release-intent" value="save-progress" :checked="releaseIntent === 'save-progress'" @change="changeReleaseIntent('save-progress')" /><span>{{ tr('仅提交代码') }}</span></label>
            </div>
            <p v-if="releaseIntent === 'save-progress'" class="section-help">{{ tr('只提交所选代码，不改版本、不创建 Tag、不构建或部署。') }}</p>
          </fieldset>
          <ReleaseBuildChoice v-if="releaseIntent === 'formal'" :plan="buildPlan" :disabled="checkingCandidate || publishing || autoSubmitting" @select="changeBuildPlan" />
          <ReleaseSyncChoice v-else code-only :policy="syncPolicy" :notice="syncNotice" :missing="syncNeedsSetup" :disabled="checkingCandidate || publishing || autoSubmitting" @change="changeSyncPolicy" @settings="switchReleaseTab('settings', true)" />
          <div v-if="releaseIntent === 'formal' && syncNeedsSetup" class="alert warn" role="alert">{{ syncNotice }} <button type="button" @click="switchReleaseTab('settings', true)">{{ tr('前往设置') }}</button></div>
          <p v-if="releaseIntent==='save-progress' && pushRemote" class="section-help">{{tr('上传可能触发仓库已有 CI。')}}</p>
          </ReleaseFormSection>
          <LocalBuildPanel v-if="localBuildMounted" v-show="localBuildVisible" embedded :app-id="app.id" :app-name="app.name" @preferences="rememberPreferences" @settings="switchReleaseTab('settings', true)" />
          <template v-if="!localBuildVisible && (preflight || releaseConfig)">
          <div v-if="configFileDirty || configEditorOpen" class="alert warn settings-edit-hint">{{ tr('高级配置尚未保存，请在对应区域保存或取消修改。') }}<button type="button" @click="switchReleaseTab('settings', true)">{{ tr('前往设置') }}</button></div>
          <section v-if="blockingIssues.length" class="issues">
            <div v-for="issue in blockingIssues" :key="issue.code" class="alert" :class="issue.code === 'staged_changes' ? 'warn staged-issue' : 'error'">
              <template v-if="issue.code === 'staged_changes'">
                <strong>{{ tr('有文件已加入 Git 待提交列表') }}</strong>
                <p>{{ tr('取消暂存后，可在这里重新选择文件。不会删除文件或撤销修改。') }}</p>
                <button type="button" :disabled="unstaging || publishing || blockingIssues.some(item => ['repository_operation', 'merge_conflict'].includes(item.code))" :aria-busy="unstaging" @click="unstageFiles">{{ unstaging ? tr('正在取消暂存…') : tr('取消暂存并重新选择文件') }}</button>
              </template>
              <template v-else>{{ tr(issue.message) }}</template>
            </div>
          </section>
          <div v-if="unstageNotice" class="alert info" role="status">{{ unstageNotice }}</div>
          <div v-if="remoteMissing" class="alert warn">{{ tr('未检测到 GitHub 远端，请在设置中配置，或选择本机操作。') }}</div>

          <ReleaseFormSection v-if="releaseIntent==='formal'" section-id="version" :title="tr('平台与版本')">
          <section ref="platformSectionRef" class="platform-section" tabindex="-1" :aria-label="tr('选择构建端')">
            <div class="platform-grid">
              <article v-for="platform in visibleProductPlatforms" :key="platform.id" class="platform-card version-platform-card" :aria-label="releaseTargetLabel(platform.name)"
                :class="{ selected: !gitOnly && platformSelected(platform), partial: !gitOnly && platformPartiallySelected(platform), unavailable: !!platformUnavailableReason(platform) }">
                <button type="button" class="platform-select" :aria-pressed="!gitOnly && platformSelected(platform)" :disabled="!!platformUnavailableReason(platform)" @click="togglePlatform(platform, !platformSelected(platform))">
                  <span class="platform-icon">{{ platform.icon }}</span>
                  <span class="platform-copy">
                    <span class="platform-title"><strong :title="releaseTargetLabel(platform.name)">{{ releaseTargetLabel(platform.name) }}</strong></span>
                    <small class="platform-meta">
                      <span v-for="version in platformVersions(platform)" :key="version.versionGroupId" class="platform-current-version" :title="`${version.versionGroupName} · ${tr('当前版本')} · ${displayCurrentVersion(version.currentVersion)}`">{{ displayCurrentVersion(version.currentVersion) }}</span>
                      <span v-if="!preflight" class="platform-current-version">{{ tr('正在读取版本…') }}</span>
                      <span v-if="platformCardDetail(platform)" class="platform-description" :title="platformCardDetail(platform)">{{ platformCardDetail(platform) }}</span>
                    </small>
                  </span>
                  <span v-if="!gitOnly && (platformSelected(platform) || platformPartiallySelected(platform))" class="chosen-mark">✓</span>
                </button>
              </article>

            </div>
          </section>

          <section v-if="preflight" class="block release-versions" :aria-label="tr('发布版本')">
            <div class="tag-switch-row"><h4>{{ tr('发布版本') }}</h4>
              <div v-if="createTag && !targetSelectionMissing" class="choice-picker version-mode-picker" role="radiogroup" :aria-label="tr('版本规则')">
                <label class="choice-option" :class="{ selected: versionMode === 'auto' }"><input v-model="versionMode" type="radio" name="release-version-mode" value="auto" @change="onVersionModeChange" /><span><strong>{{ tr('自动递增') }}</strong></span></label>
                <label class="choice-option" :class="{ selected: versionMode === 'manual' }"><input v-model="versionMode" type="radio" name="release-version-mode" value="manual" @change="onVersionModeChange" /><span><strong>{{ tr('手动设置') }}</strong></span></label>
              </div>
            </div>
            <p v-if="targetSelectionMissing" class="section-help">{{ tr('先选择平台，再确认对应的版本。') }}</p>
            <template v-else-if="createTag">
              <div class="release-version-list">
                <ReleaseVersionChange v-for="version in plannedVersions" :key="version.versionGroupId" compact :current="version.currentVersion" :target="version.targetVersion" :label="version.versionGroupName" :upgrading="true" :editable="versionMode === 'manual'" @change="onVersionInput(version.versionGroupId, $event)" />
              </div>
              <div v-if="!versionValid" class="field-error">{{ tr('版本必须是 X.Y.Z，例如 1.4.0。') }}</div>
            </template>
            <p v-else class="section-help">{{ tr('不创建版本 Tag') }}</p>
          </section>
          <div v-else class="state">{{ tr('正在读取版本…') }}</div>
          </ReleaseFormSection>

          <template v-if="preflight">
          <ReleaseFormSection section-id="confirmation" :title="releaseIntent === 'formal' ? tr('文件与发布确认') : tr('文件与提交确认')">
          <ReleaseSafetyPanel @ignore-pending="pendingIgnoreCount=$event" v-model:checks-enabled="checksEnabled" :locked="autoSubmitting || publishing || ignoringFile" :ignore-file="ignoreSafetyFile" :ignore-files="ignoreSafetyFiles" :app-id="app.id" :intent="releaseIntent" :cloud-build="releaseIntent==='formal' && buildMode==='github'" :files="safetyFiles" :selected="selected" :decisions="manualDecisions" :candidate="candidate" :busy="checkingCandidate" :stale="reviewStale" :finding-decisions="findingDecisions" :resolving-review="resolvingReview" @choose="chooseSafetyFile" @recommend="adoptRecommended" @cancel="cancelCandidate" @refresh="refreshSafety" @check="inspectCandidate" @exception="recordSensitiveException">
          <template #after-files>
          <ReleaseIgnoredFolders v-if="preflight.managedIgnores" :policy="preflight.managedIgnores" :locked="autoSubmitting || publishing || ignoringFile || checkingCandidate || !!pendingIgnoreCount" :update="updateManagedIgnores" />
          <section v-if="preflight.aheadCount" class="file-picker">
            <details v-if="preflight.aheadCount" class="unpushed-files">
              <summary>{{ tr('已提交到本机，等待上传 {0}（{1} 次提交，{2} 个文件）', [remoteDestination, preflight.aheadCount, preflight.unpushedChanges.length]) }}</summary>
              <div class="file-list committed-list">
                <div v-for="file in preflight.unpushedChanges" :key="file.path" class="file-row committed-row">
                  <span class="committed-mark">✓</span>
                  <span class="file-status" :class="{ added: file.status.startsWith('A') }">{{ file.status.startsWith('A') ? tr("新增") : file.status.startsWith('D') ? tr("删除") : file.status.startsWith('R') ? tr("改名") : tr("修改") }}</span>
                  <code :title="file.path">{{ file.path }}</code>
                </div>
              </div>
            </details>
            <p class="unpushed-note">{{ tr("这些文件已经提交，所以不会出现在上面的待提交列表中。") }}</p>
          </section>
          </template>
          </ReleaseSafetyPanel>

          <label v-if="releaseIntent==='save-progress'" class="full-label">{{tr('提交说明')}}<input v-model="commitMessage" @input="onCommitMessageInput" /></label>

          <section v-if="createTag && !targetSelectionMissing" class="release-notes">
            <div class="release-notes-head">
              <h3><label for="release-notes-input">{{ pushRemote ? tr("更新说明（将显示在 GitHub）") : tr('更新说明') }}</label></h3>
              <button type="button" :disabled="releaseNotesLoading" @click="generateReleaseNotesDraft(true)">{{ releaseNotesLoading ? tr("生成中…") : tr("重新生成") }}</button>
            </div>
            <textarea
              id="release-notes-input"
              v-model="releaseNotes"
              rows="5"
              maxlength="12000"
              :placeholder="releaseNotesLoading ? tr('正在后台生成，也可以直接填写…') : tr('请简要填写本次功能、问题修复和性能变化')"
              aria-describedby="release-notes-status"
              :aria-label="tr('更新说明')"
              @input="onReleaseNotesInput"
            ></textarea>
            <div id="release-notes-status" class="release-notes-status" role="status" aria-live="polite">
              <template v-if="releaseNotesLoading"><span class="release-loading-spinner" aria-hidden="true"></span>{{ tr('后台生成中，可先选择版本和文件。') }}</template>
              <template v-else-if="releaseNotesDirty">{{ tr('已保留手动修改，不会自动覆盖。') }}</template>
              <template v-else-if="releaseNotesGeneratedFor && !releaseNotesError">{{ tr('说明已生成，可直接修改。') }}</template>
            </div>
            <div v-if="releaseNotesStale && releaseNotesDirty" class="alert warn release-notes-alert">{{ tr("文件、构建端或版本已变化。你修改过的说明已保留，可直接修改或重新生成。") }}</div>
            <div v-if="releaseNotesError" class="alert error release-notes-alert">{{ tr("生成失败：") }}{{ releaseNotesError }} <button type="button" @click="generateReleaseNotesDraft(true)">{{ tr("重试") }}</button></div>
            <div v-else-if="!releaseNotesLoading && !releaseNotes.trim()" class="field-error">{{ tr("创建 Tag 前请填写更新说明。") }}</div>
          </section>

            <div v-if="automationBranchMismatch" class="alert warn">{{ tr('自动发布只接受 {0} 分支，当前为 {1}。', [configuredAutomation?.releaseBranch, preflight.branch]) }}</div>
            <div v-else-if="!createTag && automationTargetRequiresTag" class="alert warn">{{ tr("所选云端构建由 Tag 触发，请开启“创建版本 Tag”。") }}</div>
            <div v-else-if="!pushRemote && selectedNeedsRemotePush" class="alert warn">{{ tr("云端构建必须上传到 GitHub。") }}</div>
            <div v-else-if="invalidChosenTargetIds.length" class="alert warn">{{ tr("请为高级目标选择操作，或改为“仅提交代码”。") }}</div>
            <div v-else-if="targetSelectionMissing" class="alert warn target-selection-hint"><span>{{ tr("请选择构建端；只需保存代码时，选择顶部的“仅提交代码”。") }}</span><button type="button" @click="chooseReleaseTarget">{{ tr('选择构建端') }}</button></div>
          </ReleaseFormSection>
          </template>
          <div v-else class="state panel-detail-loading">{{ tr("正在读取版本和代码变更…") }}</div>
          </template>
        </section>
        <section v-if="historyOpen && !activeRun" id="release-panel-history" class="release-history" role="region" aria-labelledby="release-history-title">
          <template v-if="recoverablePreparations.length">
            <p class="muted">{{tr('尚未发布的准备记录')}}</p>
            <button v-for="item in recoverablePreparations" :key="item.id" type="button" class="history-row" @click="recoverPreparation(item)"><span>{{item.updatedAt}}</span><span>{{item.status === 'submitting' ? tr('结果待确认') : tr('恢复准备')}} ›</span></button>
          </template>
          <p v-if="history.length" class="muted">{{tr('最近发布（{0}）',[history.length])}}</p>
          <p v-if="!history.length" class="muted">{{tr('暂无发布记录。')}}</p>
          <button v-for="run in history" :key="run.id" type="button" class="history-row" :aria-label="tr('查看 {0} 的发布记录', [run.tagName || tr('代码提交')])" @click="openHistoryRun(run)"><code>{{ run.createTag === false ? tr('代码更新') : (run.versions?.map(version => version.tagName).join('、') || run.tagName) }}</code><span>{{run.branch}}</span><span :class="run.status">{{historyStatus(run)}} <span aria-hidden="true">›</span></span></button>
        </section>
        <section v-if="!activeRun && !loading && profileReady" v-show="releaseTab === 'settings'" id="release-panel-settings" role="tabpanel" aria-labelledby="release-tab-settings" class="release-panel settings-panel" tabindex="0">
          <div class="settings-intro"><h3>{{ tr('项目发布设置') }}</h3><p>{{ tr('构建方式、发布端和版本在“发布”中选择。') }}</p></div>
          <template v-if="preflight">
          <ReleaseSetupOverview v-if="releaseIntent === 'formal'" :app-id="app.id" :config="releaseConfig" :available="configEndpointAvailable" :disabled="configScanning || configSaving || configEditorOpen || configFileOpen || safetySettingsDirty" @saved="onConfigFileSaved" @advanced="openAdvancedSettings" @busy="configScanning = $event" />
          <details ref="advancedSettings" class="advanced-settings settings-advanced">
          <summary>{{ tr('高级设置（通常不用改）') }}<span v-if="configEditorOpen || configFileDirty" class="unsaved-dot" :aria-label="tr('有未保存的修改')"></span></summary>
          <div class="advanced-body">
          <section class="repo-card">
            <div class="kv"><span>{{ tr("代码仓库") }}</span><code>{{ preflight.repoRoot }}</code></div><div class="kv"><span>{{ tr("当前分支") }}</span><code>{{ preflight.branch || tr("未绑定分支") }}</code></div>
            <div class="kv"><span>{{ tr("远程地址") }}</span><code>{{ preflight.remoteUrl || '—' }}</code></div><div class="kv"><span>{{ tr("仓库通用 Tag（不含平台 Tag）") }}</span><code>{{ preflight.latestTag || tr("还没有版本 Tag") }}</code></div>
          </section>

          <details class="advanced"><summary>{{tr('高级：文件规则与检查')}}</summary>
          <ReleaseSafetySettings v-if="releaseConfig" :code-only="releaseIntent==='save-progress'" :config="releaseConfig" :files="safetyFiles" :busy="configSaving || configEditorOpen || configFileDirty" @save="saveSafetyConfig" @dirty="safetySettingsDirty=$event" />
          </details>
          <section v-if="releaseIntent==='formal'" class="block config-section">
            <div class="section-head">
              <div><h3>{{ tr("发布目标") }}</h3><div class="section-help">{{ tr("自动识别项目；日常发布只需勾选本次要处理的平台。") }}</div></div>
              <div v-if="configEndpointAvailable" class="toolbar"><button @click="scanReleaseConfig" :disabled="configScanning || configSaving || configEditorOpen || configFileOpen">{{ configScanning ? tr("识别中…") : tr("重新自动识别") }}</button><button @click="openConfigEditor" :disabled="configSaving || configEditorOpen || configFileOpen">{{ configEditorOpen ? tr("正在配置") : tr("修改配置") }}</button></div>
            </div>
            <div v-if="configNotice" class="alert info">{{ configNotice }}</div>
            <div v-if="releaseConfig" class="config-meta"><span>{{ releaseConfig.source === 'file' ? tr("已保存配置") : tr("自动识别建议") }}</span><code>{{ releaseConfig.configPath || '.launcher/release.yaml' }}</code></div>
            <ReleaseConfigFileEditor :app-id="app.id" :disabled="configScanning || configSaving || configEditorOpen" @saved="onConfigFileSaved" @editing="configFileOpen = $event" @dirty="configFileDirty = $event" />
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

              <div class="target-list">
                <article v-for="target in releaseConfig.targets" :key="target.id" class="target-card" :class="{ disabled: !targetAvailable(target) || gitOnly, selected: !gitOnly && targetAvailable(target) && targetChoices[target.id]?.selected, invalid: invalidChosenTargetIds.includes(target.id) }">
                  <header class="target-head"><label class="target-select"><input type="checkbox" :checked="!gitOnly && targetAvailable(target) && targetChoices[target.id]?.selected" :disabled="gitOnly || !targetAvailable(target)" @change="setTargetSelected(target.id, ($event.target as HTMLInputElement).checked)" /><span><strong>{{ target.name }}</strong><small>{{ target.kind }} · {{ versionGroupName(target) }}</small></span></label><span v-if="target.detected" class="detected-badge">{{ tr("自动识别") }}</span></header>
                  <div v-if="targetUnavailableReason(target)" class="unavailable">{{ targetUnavailableReason(target) }}</div>
                  <div v-else class="phase-grid"><label v-for="phase in phaseOptions.filter(item => phaseAllowed(item.key))" :key="phase.key" class="phase-choice" :class="{ unavailable: !target.steps[phase.key] && !(phase.key === 'publish' && target.delivery), risky: phase.risky && targetChoices[target.id]?.[phase.key] }"><input type="checkbox" :checked="targetChoices[target.id]?.[phase.key]" :disabled="gitOnly || !targetChoices[target.id]?.selected || (!target.steps[phase.key] && !(phase.key === 'publish' && target.delivery))" @change="setTargetPhase(target.id, phase.key, ($event.target as HTMLInputElement).checked)" /><span>{{ phase.label }}<small>{{ targetPhaseHint(target, phase) }}</small></span></label></div>
                  <div v-if="!gitOnly && invalidChosenTargetIds.includes(target.id)" class="target-error">{{ tr("请至少选择一个有命令的动作；也可以修改配置或选择“仅 Git”。") }}</div>
                  <div v-if="target.steps.check" class="target-check">{{ tr("发布前会先自动检查") }}</div>
                </article>
              </div>
            </template>
            <div v-else-if="configEndpointAvailable" class="empty-config"><p>{{ tr("还没有配置 PC、Web、Android 或服务端等发布目标。") }}</p><button class="primary" :disabled="configScanning" @click="scanReleaseConfig">{{ configScanning ? tr("正在分析项目…") : tr("一键自动识别项目") }}</button></div>
          </section>

          <section v-if="releaseIntent==='formal'" class="block"><h3>{{ tr("Git 与检查设置") }}</h3><div class="form-grid"><label>{{ tr("远程仓库") }}<select v-model="remoteName"><option v-for="remote in preflight.remotes" :key="remote" :value="remote">{{ remote }}</option></select></label><label>{{ tr("版本文件识别") }}<select v-model="versionStrategy"><option value="auto">{{ tr("自动识别") }}</option><option value="tauri">Tauri</option><option value="node">Node</option><option value="manual">{{ tr("不自动修改") }}</option></select></label></div><details class="advanced compact"><summary>{{ tr("高级：通用发布前检查命令") }}</summary><label class="full-label">{{ tr("命令（可选）") }}<small v-if="buildMode === 'github'">{{ tr('此命令只在本地模式执行') }}</small><input :disabled="buildMode === 'github'" v-model="preReleaseCommand" :placeholder="tr('例如 npm test')" /></label></details><button @click="saveAndRecheck" :disabled="savingProfile">{{ savingProfile ? tr('刷新信息…') : tr('保存并刷新') }}</button></section>

          <section v-if="releaseIntent==='formal'" class="block">
            <h3>{{ tr("版本与提交详情") }}</h3>
            <template v-if="createTag"><div class="strategy-line">{{ tr("将更新") }} {{ selectedVersionFiles.join('、') || tr("不修改版本文件") }}</div><div v-if="Object.keys(visibleCurrentVersions).length" class="current-versions"><code v-for="(version, file) in visibleCurrentVersions" :key="file">{{ file }}: {{ version || tr("未识别") }}</code></div><div v-for="version in plannedVersions" :key="version.versionGroupId" class="kv"><span>{{ version.versionGroupName }}</span><code>{{ version.tagName }}</code></div></template>
            <div v-else class="no-tag-note">{{ tr("本次不会修改版本文件，也不会创建或推送 Tag。") }}</div>
            <label class="full-label">{{ tr("提交说明") }}<input v-model="commitMessage" @input="onCommitMessageInput" /></label>
          </section>

          </div>
          </details>
          </template>
          <ReleaseConfigFileEditor v-else :app-id="app.id" @saved="onConfigFileSaved" @editing="configFileOpen = $event" @dirty="configFileDirty = $event" />
        </section>
      </div>

      <footer v-if="activeRun && !['queued', 'running'].includes(activeRun.status)" class="m-foot run-actions" :aria-label="tr('发布操作')">
        <p v-if="error" class="run-action-error" role="alert">{{ error }}</p>
        <p v-if="retryable && !customRetryConfirmation" class="run-action-hint">{{ savedOutputsReady && retryUpload ? tr('文件已保留，重试会继续上传，不会重新生成。') : retryGuidance }}</p>
        <p v-else-if="uploadPaused" class="run-action-hint">{{ tr('记录已保留，可从发布历史继续上传。') }}</p>
        <button v-if="activeRun.status === 'failed' && !historyOpen" type="button" class="ghost return-check" :disabled="retrying || !retryMetadataLoaded" @click="startNew">{{ connectionFailedBeforeBuild ? tr('返回发布') : tr('返回检查') }}</button>
        <div class="run-primary-actions">
          <button type="button" class="defer-release" :disabled="closeLocked" @click="closePanel">{{ tr(uploadPaused ? '稍后处理' : '关闭') }}</button>
          <button v-if="retryable" type="button" class="primary retry-submit" :disabled="retrying || !retryMetadataLoaded || !!runConnectionError" :aria-busy="retrying" @click="retry()">{{ retryButtonLabel }}</button>
        </div>
      </footer>
      <footer v-if="!historyOpen && !localBuildVisible && !activeRun && !loading && profileReady && preflight && releaseTab === 'publish'" class="m-foot" :inert="publishing">
        <p v-if="submissionFeedback" class="submission-feedback" :class="submissionFeedback.tone" :role="submissionFeedback.tone === 'error' ? 'alert' : 'status'">{{ tr(submissionFeedback.text) }}</p>
        <button ref="historyButton" type="button" class="history-button footer-action" :disabled="publishing || autoSubmitting || checkingCandidate || ignoringFile || preferenceStatus === 'saving'" @click="openHistory">{{tr('发布历史')}} <span class="history-count">{{history.length}}</span></button>
        <span v-if="releaseTab === 'publish' && releaseContentHint" id="release-content-hint" class="release-content-hint" role="status">{{ releaseContentHint }}</span>
        <button v-if="checkingCandidate && checksEnabled" :disabled="publishing" @click="cancelCandidate">{{tr('取消检查')}}</button>
        <button v-if="targetSelectionMissing" type="button" class="primary footer-action" @click="chooseReleaseTarget">{{ tr('选择构建端') }}</button>
        <button v-else-if="candidateNeedsAttention && !checkingCandidate && !autoSubmitting" type="button" class="primary resolve-issues footer-action" :disabled="publishing" @click="reviewStale ? inspectCandidate() : showReleaseIssues()">{{reviewStale?tr('重新检查'):tr('处理问题')}}</button>
        <div v-else class="publish-control">
          <button class="primary publish-submit footer-action" :disabled="!canSubmit || autoSubmitting || checkingCandidate" :aria-busy="autoSubmitting || checkingCandidate" @click="submitRelease">{{pendingIgnoreCount?tr(ignoringFile?'正在忽略…':'忽略未完成'):checkingCandidate?(resolvingReview?tr('验证处理结果…'):checksEnabled?tr(releaseIntent==='formal'?'检查发布内容…':'检查保存内容…'):tr('准备中…')):publishing ? tr("正在准备本地操作…") : createTag ? publishActionLabel : gitOnly ? (pushRemote ? tr('提交并上传') : tr('提交到本机')) : tr("确认提交并执行") }}</button>
        </div>
      </footer>
      <datalist id="target-kinds"><option value="desktop" /><option value="web" /><option value="android" /><option value="server" /><option value="custom" /></datalist><datalist id="runner-types"><option value="local" /><option value="git-push" /></datalist><datalist id="version-formats"><option value="json" /><option value="npm-lock" /><option value="cargo" /><option value="cargo-lock" /><option value="toml" /><option value="gradle" /></datalist>
      <div v-if="publishing" class="submitting-lock" role="status"><div class="submitting-message"><span class="submitting-spinner"></span><strong>{{ tr('正在准备本地操作…') }}</strong><p v-if="cloudExecutionNotice">{{ cloudExecutionNotice.text }}</p></div></div>
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

<style scoped src="./release/release-modal.css"></style>
