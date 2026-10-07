<script setup lang="ts">
import { computed } from 'vue'
import { tr } from '@/i18n'
import { artifactSizeSummary, progressLabels, releaseProgress } from '@/utils/releaseProgress'
import type { ProgressRun } from '@/utils/releaseProgress'
import type { CloudBuildStatus, ReleaseTarget } from '@/types'
import type { ReleaseDelivery, ReleaseTargetRun } from '@/types/releaseExecution'
const props = withDefaults(defineProps<{
  run: ProgressRun; targets?: ReleaseTargetRun[]; deliveries?: ReleaseDelivery[]; definitions?: ReleaseTarget[];
  artifacts?: Array<{ sizeBytes?: number }>; cloudHandoff?: boolean; cloudBuild?: CloudBuildStatus | null; localOnly?: boolean;
}>(), { targets: () => [], deliveries: () => [], definitions: () => [], artifacts: () => [] })
const progress = computed(() => releaseProgress({ ...props, deliveryPlanned: ['queued', 'running'].includes(props.run.status) && props.run.selectedTargets.some(choice => choice.publish && props.definitions.some(item => item.id === choice.targetId && item.delivery)) }))
const size = computed(() => artifactSizeSummary(props.artifacts))
const currentName = computed(() => {
  const current = progress.value.currentDelivery
  if (current) return props.run.versions?.find(version => version.versionGroupId === current.groupId)?.versionGroupName || current.groupId
  return props.definitions.find(item => item.id === progress.value.activeTarget?.targetId)?.name || progress.value.activeTarget?.targetId || ''
})
</script>

<template>
  <section class="release-progress-overview" :class="progress.state" :aria-label="tr('发布进度')">
    <div class="progress-headline" role="status" aria-live="polite" aria-atomic="true">
      <progress v-if="progress.state === 'running'" class="release-spinner" :aria-label="tr(progress.title)"></progress>
      <span v-else class="result-symbol" aria-hidden="true">{{ progress.state === 'succeeded' ? '✓' : progress.state === 'failed' ? '!' : '−' }}</span>
      <div class="headline-copy"><strong>{{ tr(progress.title, [tr(progress.phaseLabel)]) }}</strong><span v-if="currentName && progress.state !== 'succeeded'">{{ currentName }}</span></div>
      <span class="progress-badge">{{ tr(progressLabels[progress.state]) }}</span>
    </div>
    <ol class="release-step-list" :aria-label="tr('本次流程')">
      <li v-for="step in progress.steps" :key="step.id" :class="step.state" :aria-current="step.state === 'running' ? 'step' : undefined">
        <span class="step-mark" aria-hidden="true">{{ step.state === 'succeeded' ? '✓' : step.state === 'failed' ? '!' : '•' }}</span>
        <span>{{ tr(step.label) }}<small>{{ tr(progressLabels[step.state]) }}</small></span>
      </li>
    </ol>
    <div class="progress-meta">
      <span v-if="progress.next">{{ tr('接下来：{0}', [tr(progress.next)]) }}</span>
      <span v-if="deliveries.length">{{ tr('Release 已发布 {0}/{1}', [progress.published, deliveries.length]) }}</span>
      <span v-if="artifacts.length" class="artifact-total">{{ tr('产物 {0} 个 · {1}', [artifacts.length, size.size]) }}<template v-if="size.missing"> · {{ tr('{0} 个大小未知', [size.missing]) }}</template></span>
    </div>
    <p v-if="progress.error" class="progress-error" role="alert">{{ tr(progress.error) }}</p>
    <p v-if="progress.syncPending" class="sync-pending">{{ tr('GitHub 发布已完成；服务器同步尚未确认，请查看下方状态。') }}</p>
  </section>
</template>

<style scoped>
.release-progress-overview { --status-color: #85b7ff; flex-shrink: 0; padding: 14px 20px; border-bottom: 1px solid var(--border, #39424e); border-left: 4px solid var(--status-color); background: var(--bg-elev, #191e26); color: var(--text, #e5e9ef); min-width: 0; }
.release-progress-overview.succeeded { --status-color: #70e4b2; }.release-progress-overview.failed { --status-color: #ff9999; }.release-progress-overview.cancelled { --status-color: #f3c36d; }
.progress-headline { display: flex; align-items: center; gap: 12px; color: var(--status-color); }.headline-copy { display: grid; gap: 3px; min-width: 0; flex: 1; }.headline-copy strong { font-size: 16px; overflow-wrap: anywhere; }.headline-copy > span { font-size: 12px; color: var(--text-dim, #acb5c4); }
.progress-badge { border: 1px solid currentColor; border-radius: 6px; padding: 4px 9px; font-size: 12px; white-space: nowrap; }.result-symbol { font-size: 24px; font-weight: 700; }
.release-spinner { appearance: none; width: 22px; height: 22px; flex-shrink: 0; border: 3px solid #85b7ff30; border-top-color: currentColor; border-radius: 50%; background: transparent; animation: release-spin .9s linear infinite; }
.release-spinner::-webkit-progress-bar { display: none; }.release-spinner::-moz-progress-bar { background: transparent; }
@keyframes release-spin { to { transform: rotate(360deg); } }
.release-step-list { list-style: none; padding: 0; margin: 13px 0 9px; display: flex; gap: 8px 16px; flex-wrap: wrap; }.release-step-list li { display: flex; align-items: flex-start; gap: 5px; font-size: 11px; color: var(--text-dim, #acb5c4); }.release-step-list small { display: block; font-size: 10px; margin-top: 2px; }.release-step-list .succeeded { color: #70e4b2; }.release-step-list .running { color: #85b7ff; font-weight: 600; }.release-step-list .failed { color: #ff9999; }.release-step-list .cancelled { color: #f3c36d; }
.progress-meta { display: flex; flex-wrap: wrap; gap: 6px 16px; font-size: 11px; color: var(--text-dim, #acb5c4); }.artifact-total { margin-left: auto; }.progress-error { margin: 9px 0 0; font-size: 12px; color: #ff9999; overflow-wrap: anywhere; max-height: 4.5em; overflow: auto; }.sync-pending { margin: 8px 0 0; color: #f3c36d; font-size: 12px; }
@media (prefers-reduced-motion: reduce) { .release-spinner { animation: none; } }
@media (max-width: 520px) { .release-progress-overview { padding: 12px; }.headline-copy strong { font-size: 14px; }.release-step-list { gap: 7px 10px; }.release-step-list li { flex: 1 1 40%; }.artifact-total { margin-left: 0; } }
</style>
